// Lint subcommands for go-mk. This file ports scripts/go-mk-lint.sh into the
// go-mk binary: the per-gate runners (golangci-lint, format, gocyclo,
// deadcode), the capture-* helpers, the scoped lint-files and lint-diff paths,
// the fmt/vet/test/govulncheck passes, and the lint chain runner. It lives in
// package main, which owns all stdout/stderr, process execution, and file I/O;
// the pure shaping logic lives in internal/lint, internal/findings,
// internal/capture, and internal/lintgate. Functions that run a process or
// mutate files emit a structured slog event at the boundary so the
// missing_boundary_log analyzer is satisfied and the diagnostics stay on
// stderr, separate from the neutral gate report on stdout.
package main

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/capture"
	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lintgate"
	"goodkind.io/go-makefile/internal/report"
)

// goFindingPattern and goLocationPattern mirror GO_MK_GO_FINDING_PATTERN and
// GO_MK_GO_LOCATION_PATTERN from scripts/go-mk-lint.sh. The first matches a
// golangci-style "file:line:col: message" line or a trailing "(linter)" tag;
// the second matches only the leading location prefix used by deadcode and the
// scoped gates.
var (
	goFindingPattern  = regexp.MustCompile(`^[^[:space:]][^:]+:[0-9]+:[0-9]+: |^[^[:space:]].*\([[:alnum:]_-]+\)$`)
	goLocationPattern = regexp.MustCompile(`^[^[:space:]][^:]+:[0-9]+:[0-9]+:`)
)

// lintEnvDefault returns the environment variable value or the supplied
// default when the variable is empty or unset, mirroring the shell ${VAR:-def}.
func lintEnvDefault(name, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

// makeDir is the .make directory under the repository root, where the gates
// write their raw, findings, key, and failed-gate files. It mirrors the
// hardcoded ".make" the shell uses relative to the working directory.
const makeDir = ".make"

// ensureMakeDir creates the .make directory, mirroring the shell's mkdir -p
// .make before each gate. It emits a boundary log because it mutates the
// filesystem.
func ensureMakeDir() error {
	slog.Info("lint ensure make dir", slog.String("dir", makeDir))
	return os.MkdirAll(makeDir, 0o755)
}

// lintCommand is the named enum of lint-* subcommands runLint dispatches, one
// per case label ported from go-mk-lint.sh. A named string type lets the
// compiler reason about the closed set and keeps the dispatch switch off bare
// string literals, satisfying the bare-string-switch analyzer.
type lintCommand string

const (
	cmdLint                    lintCommand = "lint"
	cmdLintTools               lintCommand = "lint-tools"
	cmdLintGolangci            lintCommand = "lint-golangci"
	cmdLintGolangciScope       lintCommand = "lint-golangci-scope"
	cmdLintFormat              lintCommand = "lint-format"
	cmdLintGocyclo             lintCommand = "lint-gocyclo"
	cmdLintDeadcode            lintCommand = "lint-deadcode"
	cmdLintFiles               lintCommand = "lint-files"
	cmdLintDiff                lintCommand = "lint-diff"
	cmdFmt                     lintCommand = "fmt"
	cmdVet                     lintCommand = "vet"
	cmdTest                    lintCommand = "test"
	cmdGovulncheck             lintCommand = "govulncheck"
	cmdCaptureGolangci         lintCommand = "capture-golangci"
	cmdCaptureGolangciBaseline lintCommand = "capture-golangci-baseline"
	cmdCaptureGolangciScope    lintCommand = "capture-golangci-scope"
	cmdCaptureGocyclo          lintCommand = "capture-gocyclo"
	cmdCaptureDeadcode         lintCommand = "capture-deadcode"
	cmdStaticcheckExtra        lintCommand = "staticcheck-extra"
	cmdStaticcheckExtraBin     lintCommand = "staticcheck-extra-bin"
	cmdStaticcheckExtraCapture lintCommand = "staticcheck-extra-capture"
)

// runLint dispatches the lint-* subcommands ported from go-mk-lint.sh. It
// returns the process exit code so main can call os.Exit with it, mirroring the
// shell's per-command return status. A return of (code, false) means "no
// recognized command".
func runLint(command string, args []string) (int, bool) {
	switch lintCommand(command) {
	case cmdLint:
		return runLintChain(), true
	case cmdLintTools:
		return statusFromError(runLintTools()), true
	case cmdLintGolangci:
		return runLintGolangci(), true
	case cmdLintGolangciScope:
		return runLintGolangciScope(), true
	case cmdLintFormat:
		return runLintFormat(), true
	case cmdLintGocyclo:
		return runLintGocyclo(), true
	case cmdLintDeadcode:
		return runLintDeadcode(), true
	case cmdLintFiles:
		return runLintFiles(), true
	case cmdLintDiff:
		return runLintDiff(), true
	case cmdFmt:
		return statusFromError(runFmt()), true
	case cmdVet:
		return statusFromError(runVet()), true
	case cmdTest:
		return statusFromError(runTest()), true
	case cmdGovulncheck:
		return statusFromError(runGovulncheck()), true
	case cmdCaptureGolangci:
		return statusFromError(runCaptureGolangci(args)), true
	case cmdCaptureGolangciBaseline:
		return statusFromError(runCaptureGolangciBaseline(args)), true
	case cmdCaptureGolangciScope:
		return statusFromError(runCaptureGolangciScope(args)), true
	case cmdCaptureGocyclo:
		return statusFromError(runCaptureGocyclo(args)), true
	case cmdCaptureDeadcode:
		return statusFromError(runCaptureDeadcode(args)), true
	case cmdStaticcheckExtra:
		return runStaticcheckExtra(), true
	case cmdStaticcheckExtraBin:
		return runStaticcheckBin(), true
	case cmdStaticcheckExtraCapture:
		return runStaticcheckCapture(args), true
	default:
		return 0, false
	}
}

// statusFromError converts an error to a process exit code: nil is 0, anything
// else is 1, matching how the shell functions return non-zero on failure.
func statusFromError(err error) int {
	if err != nil {
		writeStderr("go-mk: " + err.Error() + "\n")
		return 1
	}
	return 0
}

// recordFailedGate appends a gate name to .make/lint.failed so the chain runner
// can list the failed gates, mirroring go_mk_record_failed_gate. It emits a
// boundary log because it mutates the filesystem.
func recordFailedGate(gateName string) {
	slog.Info("lint record failed gate", slog.String("gate", gateName))
	if err := os.MkdirAll(makeDir, 0o755); err != nil {
		slog.Error("lint could not create make dir", slog.String("err", err.Error()))
		return
	}
	path := filepath.Join(makeDir, "lint.failed")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Error("lint could not open failed-gate file", slog.String("err", err.Error()))
		return
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(gateName + "\n"); err != nil {
		slog.Error("lint could not write failed-gate file", slog.String("err", err.Error()))
	}
}

// splitWords splits a string on whitespace, mirroring the shell word-splitting
// go_mk_split_words performs on flag and target lists. It does not honor shell
// quoting; the make layer passes already-tokenizable strings.
func splitWords(text string) []string {
	return strings.Fields(text)
}

// captureCommand runs name with args under the lint GOMAXPROCS/GOFLAGS
// environment and writes combined output to outputPath, returning the captured
// exit status. It mirrors go_mk_run_lint_capture: a non-zero command exit is
// returned as a status, not a Go error; a real failure (exec not found, output
// write error) is returned as an error. The boundary log lives in capture.Run.
func captureCommand(name string, args []string, outputPath string) (int, error) {
	result, err := capture.Run(name, args, lintEnv(), outputPath)
	if err != nil {
		return 0, err
	}
	return result.Status, nil
}

// lintEnv builds the environment for a captured lint command, mirroring
// go_mk_run_lint_cpu: it resolves the host concurrency, sets GOMAXPROCS to it,
// and rewrites GOFLAGS to carry -p=<concurrency>. When the resolved concurrency
// is zero the shell runs the command unchanged; go-mk never resolves zero from
// the host, so this always sets the two variables.
func lintEnv() []string {
	concurrency := capture.HostConcurrency()
	env := os.Environ()
	env = setEnvVar(env, "GOMAXPROCS", strconv.Itoa(concurrency))
	env = setEnvVar(env, "GOFLAGS", capture.LintGOFLAGS(os.Getenv("GOFLAGS"), concurrency))
	// Isolate golangci-lint's content-addressed results cache per worktree so a
	// sibling worktree with byte-identical files cannot poison this run with the
	// sibling's stored absolute paths. A caller-set value is respected. Only
	// golangci-lint reads this variable, so other tools are unaffected.
	// golangci-lint requires an absolute cache path, so resolve it against the
	// working directory; if that resolution fails, leave the variable unset and
	// let golangci-lint fall back to its default cache.
	if os.Getenv("GOLANGCI_LINT_CACHE") == "" {
		if cacheDir, absErr := filepath.Abs(filepath.Join(makeDir, "golangci-cache")); absErr == nil {
			env = setEnvVar(env, "GOLANGCI_LINT_CACHE", cacheDir)
		}
	}
	return env
}

// setEnvVar replaces or appends a KEY=VALUE entry in an environment slice,
// returning the updated slice.
func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for index, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[index] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// runLintCPU runs name with args under the lint concurrency environment,
// streaming stdout and stderr to the process streams, mirroring
// go_mk_run_lint_cpu for the fmt/vet/test/govulncheck passes that print
// directly. The boundary log is emitted here because this runs a process.
func runLintCPU(name string, args []string) error {
	slog.Info("lint run cpu command", slog.String("command", name), slog.Int("args", len(args)))
	cmd := exec.Command(name, args...)
	cmd.Env = lintEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// goEnvPath returns the value of `go env <name>`, used to locate $(go env
// GOPATH)/bin for the installed tool binaries. The boundary log is emitted
// because this runs a process.
func goEnvPath(name string) (string, error) {
	slog.Info("lint resolve go env", slog.String("var", name))
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// installGoTool runs `go install <spec>` under the lint concurrency
// environment, mirroring go_mk_install_go_tool. The boundary log is emitted
// because this runs a process.
func installGoTool(spec string) error {
	words := splitWords(spec)
	if len(words) == 0 {
		writeStdout("go install: empty install spec\n")
		return errEmptyInstallSpec
	}
	slog.Info("lint install go tool", slog.String("spec", spec))
	args := append([]string{"install"}, words...)
	cmd := exec.Command("go", args...)
	cmd.Env = lintEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// extractFindings reads a raw capture file, keeps lines matching matchPattern,
// normalizes each path against the repository root, drops lines matching the
// exclude pattern, and returns the sorted unique findings, mirroring
// go_mk_extract_findings. It also drops findings whose file does not resolve to
// an existing file inside the worktree root and returns how many it dropped:
// golangci-lint's results cache is content-addressed, so an identical file
// linted earlier in a now-deleted sibling worktree is replayed with that
// worktree's path, which NormalizePath would otherwise launder into a
// repo-local-looking path and surface as a false new finding. It reads files, so
// it emits a boundary log.
func extractFindings(rawPath, matchPattern, excludePattern string) ([]string, int, error) {
	slog.Info("lint extract findings", slog.String("raw", rawPath))
	lines, err := readFileLines(rawPath)
	if err != nil {
		return nil, 0, err
	}
	pattern, err := regexp.Compile(matchPattern)
	if err != nil {
		return nil, 0, err
	}
	root := lintRoot()
	kept := make([]string, 0, len(lines))
	droppedOutOfTree := 0
	for _, line := range lines {
		if !pattern.MatchString(line) {
			continue
		}
		normalized := findings.NormalizePath(line, root, root)
		if excludePattern != "" {
			excludeRegexp, compileErr := regexp.Compile(excludePattern)
			if compileErr != nil {
				return nil, 0, compileErr
			}
			if excludeRegexp.MatchString(normalized) {
				continue
			}
		}
		if path, ok := findings.FindingPath(normalized); ok && !findingResolvesInTree(path, root) {
			droppedOutOfTree++
			continue
		}
		kept = append(kept, normalized)
	}
	return sortedUnique(kept), droppedOutOfTree, nil
}

// findingResolvesInTree reports whether the file named by a normalized finding
// path is an existing file at or under the worktree root. A relative path is
// joined to root; an absolute path is accepted only when it lies under root. The
// containment check catches an absolute foreign path, and the existence check
// catches a relative path that NormalizePath left pointing at a deleted sibling
// worktree (its top segment is the sibling's name, which is absent under root). A
// stat error other than not-exist is treated as resolved, so a transient
// filesystem error never silently drops a real finding. An empty path resolves,
// so a finding with no parseable path is kept unchanged.
func findingResolvesInTree(path, root string) bool {
	if path == "" {
		return true
	}
	rootClean := filepath.Clean(root)
	var target string
	if filepath.IsAbs(path) {
		target = filepath.Clean(path)
	} else {
		target = filepath.Clean(filepath.Join(rootClean, path))
	}
	if target != rootClean && !strings.HasPrefix(target, rootClean+string(filepath.Separator)) {
		return false
	}
	if _, statErr := os.Stat(target); statErr != nil {
		return !os.IsNotExist(statErr)
	}
	return true
}

// toolFailedWithoutFindings reports whether a non-zero tool exit with no
// surfaced findings should be treated as a tool failure. When findings were
// dropped as out-of-tree (a stale content-addressed cache replaying a deleted
// sibling worktree's paths), the non-zero exit is explained by those dropped
// findings, so the empty findings list is expected and is not a tool failure.
func toolFailedWithoutFindings(status, findingCount, droppedOutOfTree int) bool {
	return status != 0 && findingCount == 0 && droppedOutOfTree == 0
}

// outOfTreeNotice renders the user-facing line the golangci gate prints when
// extractFindings dropped findings whose paths point outside the worktree,
// which a stale content-addressed lint cache replays from a deleted sibling
// worktree.
func outOfTreeNotice(dropped int) string {
	return "ignored " + strconv.Itoa(dropped) +
		" finding(s) with out-of-tree paths (stale lint cache; run golangci-lint cache clean to clear)"
}

// lintRoot returns the normalization root, mirroring the shell GO_MK_ROOT
// default of PWD with a trailing slash. The findings package strips a leading
// pwd then cwd prefix; the shell passes pwd=$PWD/ and cwd=$GO_MK_ROOT/, which
// are the same directory here, so a single trailing-slashed root suffices.
func lintRoot() string {
	root := os.Getenv("GO_MK_ROOT")
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	if root != "" && !strings.HasSuffix(root, "/") {
		root += "/"
	}
	return root
}

// sortedUnique returns the input lines sorted with duplicates removed,
// mirroring sort -u. The returned slice is never nil.
func sortedUnique(lines []string) []string {
	seen := make(map[string]struct{}, len(lines))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

// readFileLines reads a file into one slice element per line, returning an empty
// slice when the file does not exist (mirroring the shell tolerance for a
// missing capture file). It reads a file, so it emits a boundary log.
func readFileLines(path string) ([]string, error) {
	slog.Info("lint read file", slog.String("path", path))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	text := string(data)
	if text == "" {
		return []string{}, nil
	}
	trimmed := strings.TrimSuffix(text, "\n")
	if trimmed == "" {
		return []string{}, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// writeFindingsFile writes the finding lines, one per line, to path, mirroring
// the shell capture files the gate reads back. It mutates the filesystem, so it
// emits a boundary log.
func writeFindingsFile(path string, lines []string) error {
	slog.Info("lint write findings file", slog.String("path", path))
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

// runGateAndPrint evaluates one baseline-diff gate, prints its rendered block,
// and records the gate as failed when it does not pass. It reuses
// lintgate.Evaluate and lintgate.Render so the gate text matches the shell and
// the cmd/go-mk gate subcommand byte-for-byte. It reads the baseline file, so
// it emits a boundary log. It returns true when the gate passed.
func runGateAndPrint(gateName string, current []string, baselinePath, remediation, excludePattern, scopePattern string) (bool, error) {
	return runGateAndPrintSuppress(gateName, current, baselinePath, remediation, excludePattern, scopePattern, false)
}

// runGateAndPrintSuppress is runGateAndPrint with an explicit suppressFixed flag
// that drops the "Saved findings now fixed" line on a pass, mirroring the shell
// suppress_fixed_count argument the staticcheck-extra gate sets when flags are
// enabled without a scope. It reads the baseline file, so it emits a boundary
// log. It returns true when the gate passed.
func runGateAndPrintSuppress(gateName string, current []string, baselinePath, remediation, excludePattern, scopePattern string, suppressFixed bool) (bool, error) {
	slog.Info("lint run gate", slog.String("gate", gateName), slog.String("baseline", baselinePath))
	baselineLines, err := readBaselineFindings(baselinePath, gateName, excludePattern, scopePattern)
	if err != nil {
		return false, err
	}
	excludeRegexp, scopeRegexp, err := compilePatterns(excludePattern, scopePattern)
	if err != nil {
		return false, err
	}
	result := lintgate.Evaluate(gateName, current, baselineLines, excludeRegexp, scopeRegexp, remediation)
	result.SuppressFixedCount = suppressFixed
	if gateCollecting {
		recordGateMarker(report.GateMarker{
			Name:        gateName,
			Passed:      result.Passed,
			Findings:    result.NewFindings,
			Remediation: result.Remediation,
		})
	} else {
		for _, line := range lintgate.Render(result) {
			writeStdout(line + "\n")
		}
	}
	if !result.Passed {
		recordFailedGate(gateName)
	}
	return result.Passed, nil
}

// compilePatterns compiles the exclude and scope patterns, returning nil
// regexps for empty strings so lintgate treats them as the no-filter case.
func compilePatterns(excludePattern, scopePattern string) (*regexp.Regexp, *regexp.Regexp, error) {
	var excludeRegexp, scopeRegexp *regexp.Regexp
	if excludePattern != "" {
		compiled, err := regexp.Compile(excludePattern)
		if err != nil {
			return nil, nil, err
		}
		excludeRegexp = compiled
	}
	if scopePattern != "" {
		compiled, err := regexp.Compile(scopePattern)
		if err != nil {
			return nil, nil, err
		}
		scopeRegexp = compiled
	}
	return excludeRegexp, scopeRegexp, nil
}

// readBaselineFindings reads a baseline file and extracts its findings for the
// given label, mirroring go_mk_baseline_findings: it drops blank and hash
// lines, cuts the trailing label marker, normalizes the path, drops excluded
// lines, keeps only scoped lines, and returns the sorted unique result. A
// missing baseline yields an empty slice. It reads a file, so it emits a
// boundary log.
func readBaselineFindings(path, label, excludePattern, scopePattern string) ([]string, error) {
	slog.Info("lint read baseline", slog.String("path", path), slog.String("label", label))
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	root := lintRoot()
	extracted := make([]string, 0, len(lines))
	for _, line := range lines {
		payload, ok := findings.Baseline(line, label, root, root)
		if !ok {
			continue
		}
		extracted = append(extracted, payload)
	}
	excludeRegexp, scopeRegexp, err := compilePatterns(excludePattern, scopePattern)
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(extracted))
	for _, line := range extracted {
		if excludeRegexp != nil && excludeRegexp.MatchString(line) {
			continue
		}
		if scopeRegexp != nil && !scopeRegexp.MatchString(line) {
			continue
		}
		filtered = append(filtered, line)
	}
	return sortedUnique(filtered), nil
}

// errEmptyInstallSpec mirrors the shell's empty-install-spec guard.
const errEmptyInstallSpec sentinelError = "empty install spec"
