// Staticcheck-extra orchestration for go-mk, ported from
// scripts/go-mk-staticcheck-extra.sh: resolving the analyzer binary (an explicit
// binary, a dev build from the staticcheck source repo, or a go install), then
// capturing its findings and gating them against the baseline. The analyzer
// artifact itself stays in the staticcheck/ submodule; only the orchestration
// moves here. This file lives in package main and owns process execution and
// file I/O; boundary functions emit a structured slog event.
package main

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
)

// errStaticcheckBin signals that staticcheck-extra binary resolution failed with
// a message already printed to stdout, so the dispatcher returns a non-zero exit
// without statusFromError printing a second diagnostic.
const errStaticcheckBin sentinelError = "staticcheck-extra bin resolution failed"

// staticcheckInstallDefault is the default go install spec for the analyzer,
// mirroring the shell STATICCHECK_EXTRA_INSTALL default.
const staticcheckInstallDefault = "goodkind.io/go-makefile/staticcheck/cmd/staticcheck-extra@latest"

// staticcheckOutputPath returns the resolved binary path under the repository
// root, mirroring staticcheck_output_path: ${GO_MK_ROOT:-${PWD}}/.make/
// staticcheck-extra. It is absolute so a dev build with the working directory
// set to the source repo still writes into the consumer's .make.
func staticcheckOutputPath() (string, error) {
	root := os.Getenv("GO_MK_ROOT")
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
	flagsText := os.Getenv("STATICCHECK_EXTRA_FLAGS")
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
	slog.Info("staticcheck build from repo", slog.String("repo", repo), slog.String("output", output))
	cmd := exec.Command("go", "build", "-o", output, pkg)
	cmd.Dir = repo
	cmd.Env = lintEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// staticcheckInstallBinary installs the analyzer via go install and symlinks the
// installed binary to the output path, mirroring staticcheck_install_binary. It
// runs go install with GOPROXY=direct, GONOSUMDB for the private module, and
// GOBIN set to the GOPATH bin. It runs a process, so it emits a boundary log.
func staticcheckInstallBinary() error {
	installSpec := lintEnvDefault("STATICCHECK_EXTRA_INSTALL", staticcheckInstallDefault)
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
	env := lintEnv()
	env = setEnvVar(env, "GOPROXY", "direct")
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
	walkErr := filepath.WalkDir(repo, func(walkPath string, entry os.DirEntry, walkErr error) error {
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
	})
	if walkErr != nil {
		return false, walkErr
	}
	return found, nil
}

// staticcheckResolveBin resolves the analyzer binary, mirroring
// staticcheck_resolve_bin: an explicit STATICCHECK_EXTRA_BIN wins (validated for
// executability and flag support); otherwise a dev build from
// STATICCHECK_EXTRA_BUILD_REPO when the build output is missing, stale, or
// lacking a flag; otherwise a go install of STATICCHECK_EXTRA_INSTALL, always
// reinstalling for @latest and reusing a pinned install that already supports
// the flags.
func staticcheckResolveBin() error {
	slog.Info("staticcheck resolve binary")
	configured := os.Getenv("STATICCHECK_EXTRA_BIN")
	repo := os.Getenv("STATICCHECK_EXTRA_BUILD_REPO")
	pkg := os.Getenv("STATICCHECK_EXTRA_BUILD_PKG")
	installSpec := lintEnvDefault("STATICCHECK_EXTRA_INSTALL", staticcheckInstallDefault)
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
		return nil
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
	selected, err := staticcheckSelectedBin()
	if err != nil {
		return err
	}
	if selected == "" {
		writeStdout("staticcheck-extra: not configured (skipped)\n")
		return writeFindingsFile(findingsPath, nil)
	}
	if !isExecutable(selected) {
		writeStdout("staticcheck-extra: binary " + selected + " not executable; skipping\n")
		return writeFindingsFile(findingsPath, nil)
	}
	flagArgs := splitWords(os.Getenv("STATICCHECK_EXTRA_FLAGS"))
	targetArgs, err := expandedPackageTargets(splitWords(lintEnvDefault("STATICCHECK_EXTRA_TARGETS", "./...")))
	if err != nil {
		return err
	}
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("STATICCHECK_EXTRA_EXCLUDE_PATHS"),
	)
	args := make([]string, 0, len(flagArgs)+len(targetArgs))
	args = append(args, flagArgs...)
	args = append(args, targetArgs...)
	if _, err := captureCommand(selected, args, rawPath); err != nil {
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
	rawPath := filepath.Join(makeDir, "staticcheck-extra.raw.out")
	findingsPath := filepath.Join(makeDir, "staticcheck-extra.out")
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("STATICCHECK_EXTRA_EXCLUDE_PATHS"),
	)
	scopePattern := lint.StaticcheckScopePattern(
		os.Getenv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN"),
		os.Getenv("STATICCHECK_EXTRA_FLAGS"),
	)
	suppressFixed := lint.StaticcheckSuppressFixed(os.Getenv("STATICCHECK_EXTRA_FLAGS"), scopePattern)
	if err := staticcheckCaptureFindings(rawPath, findingsPath); err != nil {
		return statusFromError(err)
	}
	current, err := readFileLines(findingsPath)
	if err != nil {
		return statusFromError(err)
	}
	passed, err := runGateAndPrintSuppress(
		"staticcheck-extra", current,
		lintEnvDefault("STATICCHECK_EXTRA_BASELINE", ".staticcheck-extra-baseline.txt"),
		"Fix the new findings before this gate will pass.",
		excludePattern, scopePattern, suppressFixed,
	)
	if err != nil {
		return statusFromError(err)
	}
	if !passed {
		return 1
	}
	return 0
}
