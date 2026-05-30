// Lint gate runners for go-mk, ported from scripts/go-mk-lint.sh. Each runner
// captures a tool's output, extracts findings, and gates them against a
// baseline, reproducing the shell's user-facing text. This file lives in
// package main and owns process execution and file I/O; boundary functions
// emit a structured slog event.
package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
)

// golangciCommand builds the golangci-lint argv for the run or fmt mode,
// mirroring golangci_command: the run mode uses GOLANGCI_LINT_RUN_FLAGS (or
// GOLANGCI_LINT_FLAGS) and the fmt mode uses GOLANGCI_LINT_FLAGS, both followed
// by GOLANGCI_LINT_TARGETS (default ./...).
func golangciCommand(mode string) (string, []string) {
	binary := lintEnvDefault("GOLANGCI_LINT", "golangci-lint")
	flagsText := lintEnvDefault("GOLANGCI_LINT_RUN_FLAGS", os.Getenv("GOLANGCI_LINT_FLAGS"))
	if mode == "fmt" {
		flagsText = os.Getenv("GOLANGCI_LINT_FLAGS")
	}
	targetsText := lintEnvDefault("GOLANGCI_LINT_TARGETS", "./...")
	args := []string{mode}
	args = append(args, splitWords(flagsText)...)
	args = append(args, splitWords(targetsText)...)
	return binary, args
}

// golangciExcludePattern resolves the golangci exclude pattern from the default
// and extra path environment variables.
func golangciExcludePattern() string {
	return lint.ExcludePattern(
		lintEnvDefault("GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("GOLANGCI_LINT_EXCLUDE_PATHS"),
	)
}

// captureGolangciFindings runs golangci-lint, captures its output to rawPath,
// extracts the findings to findingsPath, and returns the run status, mirroring
// capture_golangci_findings.
func captureGolangciFindings(rawPath, findingsPath string) (int, error) {
	binary, args := golangciCommand("run")
	status, err := captureCommand(binary, args, rawPath)
	if err != nil {
		return 0, err
	}
	extracted, err := extractFindings(rawPath, goFindingPattern.String(), golangciExcludePattern())
	if err != nil {
		return 0, err
	}
	if writeErr := writeFindingsFile(findingsPath, extracted); writeErr != nil {
		return 0, writeErr
	}
	return status, nil
}

// runLintTools installs golangci-lint, gofumpt, and goimports, mirroring
// run_lint_tools.
func runLintTools() error {
	if err := installGoTool(lintEnvDefault("GOLANGCI_LINT_INSTALL", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2")); err != nil {
		return err
	}
	if err := installGoTool(lintEnvDefault("GOFUMPT_INSTALL", "mvdan.cc/gofumpt@v0.10.0")); err != nil {
		return err
	}
	return installGoTool(lintEnvDefault("GOIMPORTS_INSTALL", "golang.org/x/tools/cmd/goimports@v0.45.0"))
}

// runLintGolangci runs the golangci-lint gate, mirroring run_lint_golangci. It
// captures findings, gates them against the baseline, and on a tool failure
// with no findings prints the FAILED block with the raw output.
func runLintGolangci() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	rawPath := filepath.Join(makeDir, "golangci-lint.raw.out")
	findingsPath := filepath.Join(makeDir, "golangci-lint.out")
	status, err := captureGolangciFindings(rawPath, findingsPath)
	if err != nil {
		return statusFromError(err)
	}
	current, err := readFileLines(findingsPath)
	if err != nil {
		return statusFromError(err)
	}
	passed, err := runGateAndPrint(
		"golangci-lint", current,
		lintEnvDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt"),
		"Fix the new findings before this gate will pass.",
		golangciExcludePattern(), "",
	)
	if err != nil {
		return statusFromError(err)
	}
	if !passed {
		return 1
	}
	if status != 0 && len(current) == 0 {
		return reportToolFailure("golangci-lint", status, rawPath)
	}
	return 0
}

// runLintGolangciScope runs and gates a single linter or rule against its slice
// of the golangci baseline, mirroring run_lint_golangci_scope. It requires a
// scope from LINTER, RULE, or GOLANGCI_LINT_BASELINE_SCOPE_PATTERN.
func runLintGolangciScope() int {
	scopePattern := lint.GolangciScopePattern(
		os.Getenv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN"),
		os.Getenv("RULE"), os.Getenv("LINTER"),
	)
	if scopePattern == "" {
		writeStdout("lint-golangci-scope: set LINTER=<name>, RULE=<name>, or GOLANGCI_LINT_BASELINE_SCOPE_PATTERN\n")
		return 2
	}
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	rawPath := filepath.Join(makeDir, "golangci-lint-scope.raw.out")
	findingsPath := filepath.Join(makeDir, "golangci-lint-scope.out")
	if err := captureGolangciScopeFindings(rawPath, findingsPath); err != nil {
		return statusFromError(err)
	}
	current, err := readFileLines(findingsPath)
	if err != nil {
		return statusFromError(err)
	}
	passed, err := runGateAndPrint(
		"golangci-lint", current,
		lintEnvDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt"),
		"Fix the new findings before this gate will pass.",
		golangciExcludePattern(), scopePattern,
	)
	if err != nil {
		return statusFromError(err)
	}
	if !passed {
		return 1
	}
	return 0
}

// captureGolangciScopeFindings runs golangci-lint, optionally narrowed to a
// LINTER via --enable-only, captures its output, extracts the findings, and
// scopes them to the resolved scope pattern, mirroring
// capture_golangci_scope_findings.
func captureGolangciScopeFindings(rawPath, findingsPath string) error {
	binary, args := golangciCommand("run")
	if linterName := os.Getenv("LINTER"); linterName != "" {
		scoped := []string{args[0], "--enable-only=" + linterName}
		scoped = append(scoped, args[1:]...)
		args = scoped
	}
	if _, err := captureCommand(binary, args, rawPath); err != nil {
		return err
	}
	extracted, err := extractFindings(rawPath, goFindingPattern.String(), golangciExcludePattern())
	if err != nil {
		return err
	}
	scopePattern := lint.GolangciScopePattern(
		os.Getenv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN"),
		os.Getenv("RULE"), os.Getenv("LINTER"),
	)
	scoped := scopeLines(extracted, scopePattern)
	return writeFindingsFile(findingsPath, scoped)
}

// scopeLines keeps only the lines matching the scope pattern, mirroring
// go_mk_scope_file. An empty pattern is the pass-through case.
func scopeLines(lines []string, scopePattern string) []string {
	if scopePattern == "" {
		return lines
	}
	regexpValue, _, err := compilePatterns("", scopePattern)
	if err != nil || regexpValue == nil {
		return lines
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if regexpValue.MatchString(line) {
			kept = append(kept, line)
		}
	}
	return kept
}

// runLintFormat runs the formatter diff gate, mirroring run_lint_format. It
// runs golangci-lint fmt --diff and fails when the diff is non-empty or the
// tool exits non-zero.
func runLintFormat() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	outputPath := filepath.Join(makeDir, "lint-format.out")
	binary, args, fmtErr := golangciFmtCommand()
	if errors.Is(fmtErr, errNoGoFiles) {
		writeStdout("lint-format: OK\n")
		writeStdout("  No Go source files in this module.\n")
		return 0
	}
	if fmtErr != nil {
		return statusFromError(fmtErr)
	}
	withDiff := []string{args[0], "--diff"}
	withDiff = append(withDiff, args[1:]...)
	status, err := captureCommand(binary, withDiff, outputPath)
	if err != nil {
		return statusFromError(err)
	}
	output, err := readFileContent(outputPath)
	if err != nil {
		return statusFromError(err)
	}
	if output != "" {
		writeStdout("golangci-lint formatters need to update:\n")
		writeStdout(output)
		if !strings.HasSuffix(output, "\n") {
			writeStdout("\n")
		}
		writeStdout("run make fmt\n")
		recordFailedGate("lint-format")
		return 1
	}
	if status != 0 {
		writeStdout("lint-format: FAILED\n")
		writeStdout("  Exit status: " + strconv.Itoa(status) + "\n")
		recordFailedGate("lint-format")
		return status
	}
	return 0
}

// captureGocycloFindings runs gocyclo, transforms its output, normalizes,
// filters, and returns the sorted unique findings plus the run status,
// mirroring capture_gocyclo_findings.
func captureGocycloFindings(rawPath, findingsPath string) (int, error) {
	threshold, _ := strconv.Atoi(lintEnvDefault("GOCYCLO_OVER", "30"))
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("GOCYCLO_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("GOCYCLO_EXCLUDE_PATHS"),
	)
	targets, err := gocycloTargets()
	if err != nil {
		return 0, err
	}
	gopathBin, err := goEnvPath("GOPATH")
	if err != nil {
		return 0, err
	}
	gocycloPath := filepath.Join(gopathBin, "bin", "gocyclo")
	args := append([]string{"-over", strconv.Itoa(threshold)}, targets...)
	status, err := captureCommand(gocycloPath, args, rawPath)
	if err != nil {
		return 0, err
	}
	rawLines, err := readFileLines(rawPath)
	if err != nil {
		return 0, err
	}
	transformed := lint.GocycloTransform(rawLines, threshold)
	root := lintRoot()
	normalized := make([]string, 0, len(transformed))
	for _, line := range transformed {
		normalized = append(normalized, findings.NormalizePath(line, root, root))
	}
	filtered := filterExcluded(normalized, excludePattern)
	result := sortedUnique(filtered)
	if writeErr := writeFindingsFile(findingsPath, result); writeErr != nil {
		return 0, writeErr
	}
	return status, nil
}

// gocycloTargets resolves the gocyclo target file list. The make default for
// GOCYCLO_TARGETS is the unexpanded command substitution
// $(find . -name "*.go" ...): make passes it as a literal string rather than
// running it, so the shell relied on word-splitting to execute the find. go-mk
// must not pass that literal to gocyclo; when GOCYCLO_TARGETS is empty or is
// that unexpanded find expression, the file list is built by walking the tree
// in Go. A real explicit GOCYCLO_TARGETS is honored by splitting it into words.
func gocycloTargets() ([]string, error) {
	targetsText := os.Getenv("GOCYCLO_TARGETS")
	if targetsText != "" && !isUnexpandedFindExpression(targetsText) {
		return splitWords(targetsText), nil
	}
	return findGoFiles()
}

// isUnexpandedFindExpression reports whether text is the make default
// GOCYCLO_TARGETS value: a $(find ...) command substitution that make left
// unexpanded. It matches the literal "$(find" prefix the shell would have run.
func isUnexpandedFindExpression(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "$(find")
}

// findGoFiles reproduces the shell default GOCYCLO_TARGETS find command by
// walking the working tree from "." in Go: it collects every *.go file, drops
// *_test.go files, and prunes the vendor, gen, and third_party directories,
// returning each path as "./<path>" the way find prints it. It reads the
// filesystem, so it emits a boundary log.
func findGoFiles() ([]string, error) {
	slog.Info("lint find go files for gocyclo")
	prunedDirs := map[string]struct{}{
		"vendor":      {},
		"gen":         {},
		"third_party": {},
	}
	files := make([]string, 0)
	walkErr := filepath.WalkDir(".", func(walkPath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if filepath.Dir(walkPath) == "." {
				if _, pruned := prunedDirs[entry.Name()]; pruned {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		files = append(files, "./"+filepath.ToSlash(walkPath))
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return files, nil
}

// filterExcluded drops lines matching the exclude pattern, mirroring
// go_mk_filter_file. An empty pattern is the pass-through case.
func filterExcluded(lines []string, excludePattern string) []string {
	if excludePattern == "" {
		return lines
	}
	regexpValue, _, err := compilePatterns(excludePattern, "")
	if err != nil || regexpValue == nil {
		return lines
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if regexpValue.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// runLintGocyclo runs the gocyclo gate, mirroring run_lint_gocyclo.
func runLintGocyclo() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	if err := installGoTool(lintEnvDefault("GOCYCLO_INSTALL", "github.com/fzipp/gocyclo/cmd/gocyclo@latest")); err != nil {
		return statusFromError(err)
	}
	rawPath := filepath.Join(makeDir, "gocyclo.raw.out")
	findingsPath := filepath.Join(makeDir, "gocyclo.out")
	status, err := captureGocycloFindings(rawPath, findingsPath)
	if err != nil {
		return statusFromError(err)
	}
	current, err := readFileLines(findingsPath)
	if err != nil {
		return statusFromError(err)
	}
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("GOCYCLO_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("GOCYCLO_EXCLUDE_PATHS"),
	)
	passed, err := runGateAndPrint(
		"gocyclo", current,
		lintEnvDefault("GOCYCLO_BASELINE", ".gocyclo-baseline.txt"),
		"Reduce the reported cyclomatic complexity before this gate will pass.",
		excludePattern, "",
	)
	if err != nil {
		return statusFromError(err)
	}
	if !passed {
		return 1
	}
	if status != 0 && len(current) == 0 {
		return reportToolFailure("gocyclo", status, rawPath)
	}
	return 0
}

// captureDeadcodeFindings runs deadcode, captures its output, and extracts the
// findings using the location pattern, mirroring capture_deadcode_findings.
func captureDeadcodeFindings(rawPath, findingsPath string) error {
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("DEADCODE_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("DEADCODE_EXCLUDE_PATHS"),
	)
	targets := splitWords(lintEnvDefault("DEADCODE_TARGETS", "./..."))
	gopathBin, err := goEnvPath("GOPATH")
	if err != nil {
		return err
	}
	deadcodePath := filepath.Join(gopathBin, "bin", "deadcode")
	if _, err := captureCommand(deadcodePath, targets, rawPath); err != nil {
		return err
	}
	extracted, err := extractFindings(rawPath, goLocationPattern.String(), excludePattern)
	if err != nil {
		return err
	}
	return writeFindingsFile(findingsPath, extracted)
}

// runLintDeadcode runs the deadcode gate, mirroring run_lint_deadcode.
func runLintDeadcode() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	if err := installGoTool(lintEnvDefault("DEADCODE_INSTALL", "golang.org/x/tools/cmd/deadcode@latest")); err != nil {
		return statusFromError(err)
	}
	rawPath := filepath.Join(makeDir, "deadcode.raw.out")
	findingsPath := filepath.Join(makeDir, "deadcode.out")
	if err := captureDeadcodeFindings(rawPath, findingsPath); err != nil {
		return statusFromError(err)
	}
	current, err := readFileLines(findingsPath)
	if err != nil {
		return statusFromError(err)
	}
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("DEADCODE_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("DEADCODE_EXCLUDE_PATHS"),
	)
	passed, err := runGateAndPrint(
		"deadcode", current,
		lintEnvDefault("DEADCODE_BASELINE", ".deadcode-baseline.txt"),
		"The deadcode lint gate found unreachable code. Remove the reported code.",
		excludePattern, "",
	)
	if err != nil {
		return statusFromError(err)
	}
	if !passed {
		return 1
	}
	return 0
}

// reportToolFailure prints the FAILED block for a tool that exited non-zero
// with no extracted findings, mirroring the shell's tool-failure tail. It reads
// the raw file, so it emits a boundary log via readFileContent.
func reportToolFailure(gateName string, status int, rawPath string) int {
	writeStdout(gateName + ": FAILED\n")
	writeStdout("  Exit status: " + strconv.Itoa(status) + "\n\n")
	writeStdout("Output:\n")
	if content, err := readFileContent(rawPath); err == nil {
		writeStdout(content)
	}
	recordFailedGate(gateName)
	return status
}

// readFileContent reads a file's full contents as a string, returning empty for
// a missing file. It reads a file, so it emits a boundary log via readFileLines
// semantics; here it reads bytes directly and logs at the boundary.
func readFileContent(path string) (string, error) {
	slog.Info("lint read file content", slog.String("path", path))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
