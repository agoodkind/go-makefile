// Staticcheck-extra orchestration resolves an explicit binary, local build,
// source installation, or verified rolling release before capturing and
// gating analyzer diagnostics.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
	"goodkind.io/go-makefile/selfupdate"
)

// errStaticcheckBin signals that staticcheck-extra binary resolution failed with
// a message already printed to stdout, so the dispatcher returns a non-zero exit
// without statusFromError printing a second diagnostic.
const errStaticcheckBin sentinelError = "staticcheck-extra bin resolution failed"

const (
	staticcheckFlagsEnv     = "STATICCHECK_EXTRA_FLAGS"
	staticcheckTestFlagsEnv = "STATICCHECK_EXTRA_TEST_FLAGS"
)

// The engine appends explicitly configured test analyzer flags.
func staticcheckFlagsText() string {
	return lint.StaticcheckFlagsText()
}

func staticcheckExcludePattern() string {
	defaultPatterns := lint.StaticcheckDefaultExcludePaths(
		lintEnvDefault("STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		staticcheckFlagsText(),
	)

	return lint.ExcludePattern(defaultPatterns, os.Getenv("STATICCHECK_EXTRA_EXCLUDE_PATHS"))
}

// staticcheckOutputPath returns the resolved binary path under the repository
// root, mirroring staticcheck_output_path: ${_GO_MK_ROOT:-${PWD}}/.make/
// staticcheck-extra. It is absolute so a dev build with the working directory
// set to the source repo still writes into the consumer's .make.
func staticcheckOutputPath() (string, error) {
	root := os.Getenv("_GO_MK_ROOT")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}

		root = wd
	}

	return filepath.Join(root, makeDir, "staticcheck-extra"), nil
}

// staticcheckMissingFlags reports whether the candidate binary fails to advertise
// every flag in STATICCHECK_EXTRA_FLAGS, mirroring staticcheck_missing_flags: it
// runs the candidate with -flags and checks each requested flag name appears on a
// "Name" line. A binary that cannot run is treated as missing every flag. It runs
// a process, so it emits a boundary log.
func staticcheckMissingFlags(candidate string) bool {
	flagsText := staticcheckFlagsText()
	if strings.TrimSpace(flagsText) == "" {
		return false
	}

	slog.Info("staticcheck probe flags", slog.String("binary", candidate))
	out, err := exec.Command(candidate, "-flags").CombinedOutput()
	if err != nil {
		return true
	}

	text := string(out)

	for _, word := range strings.Fields(flagsText) {
		name := strings.TrimLeft(word, "-")
		pattern := regexp.MustCompile(`Name.*` + regexp.QuoteMeta(name))
		if !pattern.MatchString(text) {
			return true
		}
	}

	return false
}

// staticcheckBuildFromRepo builds the analyzer from the source repo into the
// output path, mirroring staticcheck_build_from_repo. It runs go build with the
// working directory set to the source repo and the output path absolute, under
// the lint concurrency environment. It runs a process, so it emits a boundary
// log.
func staticcheckBuildFromRepo() error {
	output, err := staticcheckOutputPath()
	if err != nil {
		return err
	}

	repo := os.Getenv("STATICCHECK_EXTRA_BUILD_REPO")
	pkg := lintEnvDefault("STATICCHECK_EXTRA_BUILD_PKG", "./cmd/staticcheck-extra")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}

	slog.Info(
		"staticcheck build from repo",
		slog.String("repo", repo),
		slog.String("output", output),
	)
	cmd := exec.Command("go", "build", "-o", output, pkg)
	cmd.Dir = repo
	cmd.Env = hostLintEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// staticcheckInstallBinary installs the analyzer via go install and symlinks the
// installed binary to the output path, mirroring staticcheck_install_binary. It
// runs go install with GONOSUMDB for the analyzer modules and GOBIN set to the
// GOPATH bin. It runs a process, so it emits a boundary log.
func staticcheckInstallBinary() error {
	installSpec := os.Getenv("STATICCHECK_EXTRA_INSTALL")
	if installSpec == "" {
		return fmt.Errorf("STATICCHECK_EXTRA_INSTALL is required for a source installation")
	}

	binaryName := filepath.Base(strings.SplitN(installSpec, "@", 2)[0])
	gopath, err := goEnvPath("GOPATH")
	if err != nil {
		return err
	}

	goBin := filepath.Join(gopath, "bin")
	installedPath := filepath.Join(goBin, binaryName)
	output, err := staticcheckOutputPath()
	if err != nil {
		return err
	}

	slog.Info("staticcheck install binary", slog.String("spec", installSpec))
	cmd := exec.Command("go", "install", installSpec)
	env := hostLintEnv()
	env = setEnvVar(env, "GONOSUMDB", "goodkind.io/go-makefile,goodkind.io/go-makefile/staticcheck")
	env = setEnvVar(env, "GOBIN", goBin)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}

	_ = os.Remove(output)

	return os.Symlink(installedPath, output)
}

// staticcheckNewerSource reports whether any .go file under repo is newer than
// the output binary, mirroring the shell `find repo -name "*.go" -newer output`
// staleness check. A missing output is treated as stale. It reads the
// filesystem, so it emits a boundary log.
func staticcheckNewerSource(repo, output string) (bool, error) {
	info, err := os.Stat(output)
	if err != nil {
		return true, nil
	}

	outputModTime := info.ModTime()
	slog.Info("staticcheck scan source staleness", slog.String("repo", repo))
	found := false
	walkErr := filepath.WalkDir(
		repo,
		func(walkPath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if entry.IsDir() {
				return nil
			}

			if !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}

			fileInfo, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}

			if fileInfo.ModTime().After(outputModTime) {
				found = true
				return filepath.SkipAll
			}

			return nil
		},
	)
	if walkErr != nil {
		return false, walkErr
	}

	return found, nil
}

// staticcheckResolveBin uses an explicit binary or local checkout before
// installing the analyzer from a source specification or rolling release.
func staticcheckResolveBin() error {
	slog.Info("staticcheck resolve binary")
	configured := os.Getenv("STATICCHECK_EXTRA_BIN")
	repo := os.Getenv("STATICCHECK_EXTRA_BUILD_REPO")
	pkg := os.Getenv("STATICCHECK_EXTRA_BUILD_PKG")
	installSpec := os.Getenv("STATICCHECK_EXTRA_INSTALL")
	output, err := staticcheckOutputPath()
	if err != nil {
		return err
	}

	if configured != "" {
		if !isExecutable(configured) {
			writeStdout("staticcheck-extra: " + configured + " not executable\n")
			return errStaticcheckBin
		}

		if staticcheckMissingFlags(configured) {
			writeStdout("staticcheck-extra: " + configured + " does not support requested flags\n")
			return errStaticcheckBin
		}

		return nil
	}

	if repo != "" {
		info, statErr := os.Stat(repo)
		if statErr != nil || !info.IsDir() {
			writeStdout("staticcheck-extra: build repo " + repo + " not present; skipping\n")
			return nil
		}

		if pkg == "" {
			writeStdout("staticcheck-extra: STATICCHECK_EXTRA_BUILD_PKG not set\n")
			return errStaticcheckBin
		}

		stale := !isExecutable(output)
		if !stale {
			newer, newerErr := staticcheckNewerSource(repo, output)
			if newerErr != nil {
				return newerErr
			}

			stale = newer
		}

		if !stale {
			stale = staticcheckMissingFlags(output)
		}

		if stale {
			return staticcheckBuildFromRepo()
		}

		return nil
	}

	if installSpec == "" {
		return staticcheckInstallRelease()
	}

	if strings.HasSuffix(installSpec, "@latest") {
		return staticcheckInstallBinary()
	}

	binaryName := filepath.Base(strings.SplitN(installSpec, "@", 2)[0])
	gopath, err := goEnvPath("GOPATH")
	if err != nil {
		return err
	}

	installedPath := filepath.Join(gopath, "bin", binaryName)
	if !isExecutable(installedPath) || staticcheckMissingFlags(installedPath) {
		return staticcheckInstallBinary()
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}

	_ = os.Remove(output)

	return os.Symlink(installedPath, output)
}

func staticcheckInstallRelease() error {
	slog.Info("staticcheck install rolling release")
	output, err := staticcheckOutputPath()
	if err != nil {
		return err
	}

	directory := filepath.Dir(output)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}

	cache := filepath.Join(directory, "staticcheck-release")
	installed, err := selfupdate.InstallReleaseBinaries(
		context.Background(),
		selfupdate.InstallReleaseBinariesOptions{
			Channel: selfupdate.ReleaseChannelRolling,
			BinDir:  directory,
			Options: []selfupdate.Options{
				{
					Config: selfupdate.Config{
						Repo:          goMkSelfUpdateRepo,
						Binary:        "staticcheck-extra",
						WebBaseURLEnv: "STATICCHECK_EXTRA_RELEASE_BASE_URL",
						ValidateArgs:  append(splitWords(staticcheckFlagsText()), "-flags"),
						ValidateMatch: "\"Name\"",
					},
					CacheDir:  cache,
					StatePath: filepath.Join(cache, "state.json"),
				},
			},
		},
	)
	if err != nil {
		return err
	}

	slog.Info("staticcheck installed release", slog.String("tag", installed[0].Tag))

	return nil
}

// staticcheckSelectedBin returns the binary the gate runs, mirroring
// staticcheck_selected_bin: an explicit STATICCHECK_EXTRA_BIN, otherwise the
// build output when it is executable, otherwise the empty string.
func staticcheckSelectedBin() (string, error) {
	if configured := os.Getenv("STATICCHECK_EXTRA_BIN"); configured != "" {
		return configured, nil
	}

	output, err := staticcheckOutputPath()
	if err != nil {
		return "", err
	}

	if isExecutable(output) {
		return output, nil
	}

	return "", nil
}

// staticcheckCaptureFindings runs the analyzer and writes the normalized,
// excluded, sorted-unique findings, mirroring staticcheck_capture_findings.
// Unlike the golangci capture it normalizes the raw output directly without a
// finding-pattern match, since the analyzer prints only findings. A missing or
// non-executable binary writes an empty findings file and prints the neutral
// skip line.
func staticcheckCaptureFindings(rawPath, findingsPath string) error {
	return captureStaticcheckFindings(rawPath, findingsPath, captureStandard)
}

func captureStaticcheckFindings(rawPath, findingsPath string, mode captureMode) error {
	selected, err := staticcheckSelectedBin()
	if err != nil {
		return err
	}

	if mode == captureNotice && (selected == "" || !isExecutable(selected)) {
		return fmt.Errorf("notice requires an executable staticcheck-extra analyzer")
	}

	if selected == "" {
		writeStdout("staticcheck-extra: not configured (skipped)\n")
		return writeFindingsFile(findingsPath, nil)
	}

	if !isExecutable(selected) {
		writeStdout("staticcheck-extra: binary " + selected + " not executable; skipping\n")
		return writeFindingsFile(findingsPath, nil)
	}

	flagArgs := splitWords(staticcheckFlagsText())
	targetArgs, err := expandedPackageTargets(
		splitWords(lintEnvDefault("STATICCHECK_EXTRA_TARGETS", "./...")),
	)
	if err != nil {
		return err
	}

	excludePattern := staticcheckExcludePattern()
	args := make([]string, 0, len(flagArgs)+len(targetArgs))
	args = append(args, flagArgs...)
	args = append(args, targetArgs...)
	status, err := captureCommand(selected, args, rawPath)
	if err != nil {
		return err
	}

	rawLines, err := readFileLines(rawPath)
	if err != nil {
		return err
	}

	root := lintRoot()

	normalized := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		normalized = append(normalized, findings.NormalizePath(line, root, root))
	}

	if status == staticcheckBuildFailureStatus {
		return staticcheckBuildFailure(normalized)
	}

	if mode == captureNotice && status != 0 && status != staticcheckDiagnosticsStatus {
		return fmt.Errorf(
			"notice staticcheck-extra capture failed (exit status %d):\n%s",
			status,
			strings.Join(normalized, "\n"),
		)
	}

	filtered := filterExcluded(normalized, excludePattern)

	return writeFindingsFile(findingsPath, sortedUnique(filtered))
}

// runStaticcheckBin resolves the analyzer binary, mirroring the shell `bin`
// command. It returns 1 without a second diagnostic when resolution failed with
// a message already printed.
func runStaticcheckBin() int {
	if err := staticcheckResolveBin(); err != nil {
		if errors.Is(err, errStaticcheckBin) {
			return 1
		}

		return statusFromError(err)
	}

	return 0
}

// runStaticcheckCapture is the staticcheck-extra-capture dispatcher, mirroring
// the shell `capture` arm: it writes the raw and findings files to the supplied
// positional paths or the .make defaults, without running the baseline gate.
func runStaticcheckCapture(args []string) int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}

	rawPath := captureArg(args, 0, filepath.Join(makeDir, "staticcheck-extra.raw.out"))
	findingsPath := captureArg(args, 1, filepath.Join(makeDir, "staticcheck-extra.out"))

	return statusFromError(staticcheckCaptureFindings(rawPath, findingsPath))
}

// runStaticcheckExtra runs the staticcheck-extra gate, mirroring
// staticcheck_run_gate: it captures the analyzer findings, resolves the baseline
// scope pattern and the suppress-fixed flag, and gates the findings against the
// baseline.
func runStaticcheckExtra() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}

	runNoticeFor(noticeGateStaticcheck)
	// Resolve (build or install) the analyzer binary in-process, the work the
	// staticcheck-extra-bin make prerequisite used to do, so the gate is one
	// self-contained go-mk process. The aggregate run resolves it once up front
	// in prepareChecks and sets checksToolsPrepared, so the gate skips it there.
	if !checksToolsPrepared {
		if status := runStaticcheckBin(); status != 0 {
			return status
		}
	}

	rawPath := filepath.Join(makeDir, "staticcheck-extra.raw.out")
	findingsPath := filepath.Join(makeDir, "staticcheck-extra.out")
	excludePattern := staticcheckExcludePattern()
	scopePattern := lint.StaticcheckScopePattern(
		os.Getenv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN"),
		staticcheckFlagsText(),
	)
	suppressFixed := lint.StaticcheckSuppressFixed(staticcheckFlagsText(), scopePattern)
	if err := staticcheckCaptureFindings(rawPath, findingsPath); err != nil {
		var buildErr *staticcheckBuildError
		if errors.As(err, &buildErr) {
			return reportStaticcheckBuildFailure(buildErr)
		}

		return statusFromError(err)
	}

	current, err := readFileLines(findingsPath)
	if err != nil {
		return statusFromError(err)
	}

	passed, err := runGateAndPrintSuppress(
		"staticcheck-extra",
		current,
		lintEnvDefault("STATICCHECK_EXTRA_BASELINE", ".staticcheck-extra-baseline.txt"),
		"Fix the new findings before this gate will pass.",
		excludePattern,
		scopePattern,
		suppressFixed,
	)
	if err != nil {
		return statusFromError(err)
	}

	if !passed {
		return 1
	}

	return 0
}
