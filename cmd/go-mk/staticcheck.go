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
	"goodkind.io/go-makefile/internal/report"
)

// errStaticcheckBin signals that staticcheck-extra binary resolution failed with
// a message already printed to stdout, so the dispatcher returns a non-zero exit
// without statusFromError printing a second diagnostic.
const errStaticcheckBin sentinelError = "staticcheck-extra bin resolution failed"

// staticcheckInstallDefault is the default go install spec for the analyzer,
// mirroring the shell STATICCHECK_EXTRA_INSTALL default.
const staticcheckInstallDefault = "goodkind.io/go-makefile/staticcheck/cmd/staticcheck-extra@latest"

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
	return staticcheckBinaryLacksFlags(candidate, os.Getenv("STATICCHECK_EXTRA_FLAGS"))
}

// staticcheckBinaryLacksFlags reports whether the candidate binary fails to
// advertise every flag in flagsText. An empty flagsText lacks no flag. It emits
// a boundary log before it runs the candidate.
func staticcheckBinaryLacksFlags(candidate, flagsText string) bool {
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
	normalized, err := staticcheckRunAnalyzers(selected, flagArgs, targetArgs, rawPath)
	if err != nil {
		return err
	}
	filtered := filterExcluded(normalized, excludePattern)
	return writeFindingsFile(findingsPath, sortedUnique(filtered))
}

// staticcheckRunAnalyzers runs the analyzer binary with the given flags over the
// package targets, writes the combined output to rawPath, and returns each
// output line with its path normalized against the repository root.
func staticcheckRunAnalyzers(binary string, flagArgs, targetArgs []string, rawPath string) ([]string, error) {
	args := make([]string, 0, len(flagArgs)+len(targetArgs))
	args = append(args, flagArgs...)
	args = append(args, targetArgs...)
	if _, err := captureCommand(binary, args, rawPath); err != nil {
		return nil, err
	}
	rawLines, err := readFileLines(rawPath)
	if err != nil {
		return nil, err
	}
	root := lintRoot()
	normalized := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		normalized = append(normalized, findings.NormalizePath(line, root, root))
	}
	return normalized, nil
}

// staticcheckAdvisoryDisplayLimit caps the advisory findings one report shows.
// The findings file lists every advisory finding.
const staticcheckAdvisoryDisplayLimit = 20

// staticcheckAdvisoryRemediation is the fix hint printed under the advisory
// findings.
const staticcheckAdvisoryRemediation = "Advisory findings do not fail this gate. Fix them before the analyzers become blocking."

// staticcheckAdvisoryFindingsPath is the file that lists every advisory finding
// of the most recent run.
func staticcheckAdvisoryFindingsPath() string {
	return filepath.Join(makeDir, "staticcheck-extra-advisory.out")
}

// staticcheckCaptureAdvisory runs the analyzers in
// STATICCHECK_EXTRA_ADVISORY_FLAGS and returns their findings. The advisory
// analyzers report in _test.go files. The gated run excludes _test.go findings.
// This run applies only STATICCHECK_EXTRA_EXCLUDE_PATHS. It returns no findings
// when the flag list is empty, when no analyzer binary is resolved, or when the
// binary predates a listed analyzer.
func staticcheckCaptureAdvisory() ([]string, error) {
	flagsText := os.Getenv("STATICCHECK_EXTRA_ADVISORY_FLAGS")
	if strings.TrimSpace(flagsText) == "" {
		return nil, nil
	}
	selected, err := staticcheckSelectedBin()
	if err != nil {
		return nil, err
	}
	if selected == "" || !isExecutable(selected) {
		return nil, nil
	}
	if staticcheckBinaryLacksFlags(selected, flagsText) {
		slog.Warn("staticcheck advisory analyzers skipped; binary lacks a listed flag",
			slog.String("binary", selected), slog.String("flags", flagsText))
		return nil, nil
	}
	targetArgs, err := expandedPackageTargets(splitWords(lintEnvDefault("STATICCHECK_EXTRA_TARGETS", "./...")))
	if err != nil {
		return nil, err
	}
	rawPath := filepath.Join(makeDir, "staticcheck-extra-advisory.raw.out")
	normalized, err := staticcheckRunAnalyzers(selected, splitWords(flagsText), targetArgs, rawPath)
	if err != nil {
		return nil, err
	}
	located := make([]string, 0, len(normalized))
	for _, line := range normalized {
		if goLocationPattern.MatchString(line) {
			located = append(located, line)
		}
	}
	excludePattern := lint.ExcludePattern("", os.Getenv("STATICCHECK_EXTRA_EXCLUDE_PATHS"))
	advisory := sortedUnique(filterExcluded(located, excludePattern))
	if err := writeFindingsFile(staticcheckAdvisoryFindingsPath(), advisory); err != nil {
		return nil, err
	}
	return advisory, nil
}

// staticcheckAdvisoryDisplay returns the advisory findings one report shows: the
// first staticcheckAdvisoryDisplayLimit findings, then one line that gives the
// count of the remaining findings and the file that lists them.
func staticcheckAdvisoryDisplay(advisory []string) (shown []string, overflowNote string) {
	if len(advisory) <= staticcheckAdvisoryDisplayLimit {
		return advisory, ""
	}
	remaining := len(advisory) - staticcheckAdvisoryDisplayLimit
	note := "... " + itoa(remaining) + " more advisory finding" + findingPlural(remaining) +
		" in " + staticcheckAdvisoryFindingsPath()
	return advisory[:staticcheckAdvisoryDisplayLimit], note
}

// staticcheckReportAdvisory shows the advisory findings of a gate that passed.
// A collecting run records them on the gate marker, and the chain renders the
// step as ADVISORY. A standalone run prints them under the gate block.
func staticcheckReportAdvisory(advisory []string) {
	if len(advisory) == 0 {
		return
	}
	if gateCollecting {
		recordGateMarker(report.GateMarker{
			Name:        "staticcheck-extra",
			Passed:      true,
			Advisory:    advisory,
			Remediation: staticcheckAdvisoryRemediation,
		})
		return
	}
	shown, overflowNote := staticcheckAdvisoryDisplay(advisory)
	writeStdout("staticcheck-extra: " + itoa(len(advisory)) + " advisory finding" +
		findingPlural(len(advisory)) + " (not gated)\n")
	for _, line := range shown {
		writeStdout(line + "\n")
	}
	if overflowNote != "" {
		writeStdout(overflowNote + "\n")
	}
	writeStdout("  " + staticcheckAdvisoryRemediation + "\n")
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
	advisory, err := staticcheckCaptureAdvisory()
	if err != nil {
		return statusFromError(err)
	}
	staticcheckReportAdvisory(advisory)
	return 0
}
