// Scoped lint paths and the chain runner for go-mk, ported from
// scripts/go-mk-lint.sh: lint-files, lint-diff, the fmt/vet/test/govulncheck
// passes, the capture-* dispatchers, and run_lint_chain. This file lives in
// package main and owns process execution and file I/O; boundary functions
// emit a structured slog event.
package main

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
)

// errNoGoFiles signals that the current module owns no Go source files, so the
// formatter has nothing to format and the format gate passes vacuously.
var errNoGoFiles = errors.New("no go files in module")

// golangciFmtCommand builds the golangci-lint fmt argv scoped to the files the
// current module owns. golangci-lint fmt walks the filesystem rather than
// resolving packages through go list, so passing ./... makes it descend into
// nested modules (a vendored git submodule or a sibling module with its own
// go.mod) and format foreign files. Scoping to the go-list file set keeps fmt
// within the current module, matching what every run linter already does.
// GOLANGCI_FMT_FILES overrides the computed list. It returns errNoGoFiles when
// the module owns no Go files so callers pass the gate without invoking fmt.
func golangciFmtCommand() (string, []string, error) {
	binary := lintEnvDefault("GOLANGCI_LINT", "golangci-lint")
	flagsText := os.Getenv("GOLANGCI_LINT_FLAGS")
	var files []string
	if override := os.Getenv("GOLANGCI_FMT_FILES"); override != "" {
		files = splitWords(override)
	} else {
		resolved, err := moduleGoFiles(splitWords(lintEnvDefault("GOLANGCI_LINT_TARGETS", "./...")))
		if err != nil {
			return "", nil, err
		}
		files = resolved
	}
	if len(files) == 0 {
		return "", nil, errNoGoFiles
	}
	args := []string{"fmt"}
	args = append(args, splitWords(flagsText)...)
	args = append(args, files...)
	return binary, args, nil
}

// moduleGoFiles enumerates the .go files the current module owns for the given
// package targets via go list, including build-tag-ignored files so platform
// sources stay formatted. go list never descends into nested modules, so this
// is the structural set of files the module owns. Files that resolve to a
// path under a nested git working tree are dropped defensively, since a
// worktree of the same repo shares the module path and would otherwise be
// formatted as if it were the current module. It runs go, so it emits a
// boundary log.
func moduleGoFiles(targets []string) ([]string, error) {
	slog.Info("lint list module go files for fmt")
	const listTemplate = `{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}
{{end}}{{range .CgoFiles}}{{$d}}/{{.}}
{{end}}{{range .TestGoFiles}}{{$d}}/{{.}}
{{end}}{{range .XTestGoFiles}}{{$d}}/{{.}}
{{end}}{{range .IgnoredGoFiles}}{{$d}}/{{.}}
{{end}}`
	listArgs := append([]string{"list", "-f", listTemplate}, targets...)
	out, err := exec.Command("go", listArgs...).Output()
	if err != nil {
		return nil, err
	}
	roots, err := nestedWorktreeRoots()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0)
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(roots) > 0 {
		relLines := make([]string, 0, len(lines))
		absByRel := make(map[string]string, len(lines))
		for _, line := range lines {
			rel, relErr := filepath.Rel(cwd, line)
			if relErr != nil {
				relLines = append(relLines, line)
				absByRel[line] = line
				continue
			}
			key := "./" + filepath.ToSlash(rel)
			relLines = append(relLines, key)
			absByRel[key] = line
		}
		kept := dropPathsUnderNestedWorktrees(relLines, roots)
		out := make([]string, 0, len(kept))
		for _, key := range kept {
			out = append(out, absByRel[key])
		}
		lines = out
	}
	return sortedUnique(lines), nil
}

// runFmt applies the configured Go formatters scoped to the current module,
// mirroring run_fmt. It no-ops when the module owns no Go files.
func runFmt() error {
	if err := ensureLintTools(); err != nil {
		return err
	}
	binary, args, err := golangciFmtCommand()
	if errors.Is(err, errNoGoFiles) {
		return nil
	}
	if err != nil {
		return err
	}
	return runLintCPU(binary, args)
}

// runVet runs go vet, mirroring run_vet.
func runVet() error {
	targets := splitWords(lintEnvDefault("GO_VET_TARGETS", "./..."))
	return runLintCPU("go", append([]string{"vet"}, targets...))
}

// runTest runs go test, mirroring run_test. When GO_TEST_LDFLAGS is set it is
// inserted as a single -ldflags argv element so a multi-word stamping value
// (several -X directives) reaches `go test` intact. GO_TEST_TARGETS stays
// whitespace-split for package and flag lists, which cannot carry a quoted
// multi-word value.
func runTest() error {
	targets := splitWords(lintEnvDefault("GO_TEST_TARGETS", "./..."))
	args := append([]string{"test"}, testLdflagsArgs()...)
	args = append(args, targets...)
	return runLintCPU("go", args)
}

// testLdflagsArgs returns the -ldflags flag and its value as two argv elements
// when GO_TEST_LDFLAGS is set, keeping the multi-word value unsplit. It returns
// nil when the variable is empty so the default invocation is unchanged.
func testLdflagsArgs() []string {
	value := strings.TrimSpace(os.Getenv("GO_TEST_LDFLAGS"))
	if value == "" {
		return nil
	}
	return []string{"-ldflags", value}
}

// runGovulncheck installs and runs govulncheck, mirroring run_govulncheck.
func runGovulncheck() error {
	if err := installGoTool("golang.org/x/vuln/cmd/govulncheck@latest"); err != nil {
		return err
	}
	gopathBin, err := goEnvPath("GOPATH")
	if err != nil {
		return err
	}
	targets := splitWords(lintEnvDefault("GOVULNCHECK_TARGETS", "./..."))
	return runLintCPU(filepath.Join(gopathBin, "bin", "govulncheck"), targets)
}

// runCaptureGolangci is the capture-golangci dispatcher, mirroring the shell
// case arm. It writes the raw and findings files.
func runCaptureGolangci(args []string) error {
	if err := ensureMakeDir(); err != nil {
		return err
	}
	rawPath := captureArg(args, 0, filepath.Join(makeDir, "golangci-lint.raw.out"))
	findingsPath := captureArg(args, 1, filepath.Join(makeDir, "golangci-lint.out"))
	_, _, err := captureGolangciFindings(rawPath, findingsPath)
	return err
}

// runCaptureGolangciBaseline is the capture-golangci-baseline dispatcher,
// mirroring capture_golangci_baseline_findings: it runs golangci-lint
// GOLANGCI_LINT_BASELINE_RUNS times (default 3), concatenates the raw and
// findings outputs, and writes the sorted unique union to the findings file.
func runCaptureGolangciBaseline(args []string) error {
	if err := ensureMakeDir(); err != nil {
		return err
	}
	rawPath := captureArg(args, 0, filepath.Join(makeDir, "golangci-lint-baseline.raw.out"))
	findingsPath := captureArg(args, 1, filepath.Join(makeDir, "golangci-lint-baseline.out"))
	runCount := 3
	if value := os.Getenv("GOLANGCI_LINT_BASELINE_RUNS"); value != "" {
		if parsed, err := atoiSafe(value); err == nil && parsed > 0 {
			runCount = parsed
		}
	}
	allRaw := make([]string, 0)
	allFindings := make([]string, 0)
	for runIndex := 1; runIndex <= runCount; runIndex++ {
		runRaw := filepath.Join(makeDir, "golangci-lint-baseline."+itoa(runIndex)+".raw.out")
		runFindings := filepath.Join(makeDir, "golangci-lint-baseline."+itoa(runIndex)+".out")
		if _, _, err := captureGolangciFindings(runRaw, runFindings); err != nil {
			return err
		}
		rawLines, err := readFileLines(runRaw)
		if err != nil {
			return err
		}
		findingLines, err := readFileLines(runFindings)
		if err != nil {
			return err
		}
		allRaw = append(allRaw, rawLines...)
		allFindings = append(allFindings, findingLines...)
	}
	if err := writeFindingsFile(rawPath, allRaw); err != nil {
		return err
	}
	return writeFindingsFile(findingsPath, sortedUnique(allFindings))
}

// runCaptureGolangciScope is the capture-golangci-scope dispatcher.
func runCaptureGolangciScope(args []string) error {
	if err := ensureMakeDir(); err != nil {
		return err
	}
	rawPath := captureArg(args, 0, filepath.Join(makeDir, "golangci-lint-scope.raw.out"))
	findingsPath := captureArg(args, 1, filepath.Join(makeDir, "golangci-lint-scope.out"))
	return captureGolangciScopeFindings(rawPath, findingsPath)
}

// runCaptureGocyclo is the capture-gocyclo dispatcher; it installs gocyclo
// first, mirroring the shell case arm.
func runCaptureGocyclo(args []string) error {
	if err := ensureMakeDir(); err != nil {
		return err
	}
	if err := installGoTool(lintEnvDefault("GOCYCLO_INSTALL", "github.com/fzipp/gocyclo/cmd/gocyclo@latest")); err != nil {
		return err
	}
	rawPath := captureArg(args, 0, filepath.Join(makeDir, "gocyclo.raw.out"))
	findingsPath := captureArg(args, 1, filepath.Join(makeDir, "gocyclo.out"))
	_, err := captureGocycloFindings(rawPath, findingsPath)
	return err
}

// runCaptureDeadcode is the capture-deadcode dispatcher; it installs deadcode
// first, mirroring the shell case arm.
func runCaptureDeadcode(args []string) error {
	if err := ensureMakeDir(); err != nil {
		return err
	}
	if err := installGoTool(lintEnvDefault("DEADCODE_INSTALL", "golang.org/x/tools/cmd/deadcode@latest")); err != nil {
		return err
	}
	rawPath := captureArg(args, 0, filepath.Join(makeDir, "deadcode.raw.out"))
	findingsPath := captureArg(args, 1, filepath.Join(makeDir, "deadcode.out"))
	return captureDeadcodeFindings(rawPath, findingsPath)
}

// captureArg returns the positional capture-output argument at index or the
// fallback when it is absent, mirroring the ${2:-default} ${3:-default} shell
// defaults.
func captureArg(args []string, index int, fallback string) string {
	if index < len(args) && args[index] != "" {
		return args[index]
	}
	return fallback
}

// runLintFiles runs the scoped lint against LINT_FILES, mirroring
// run_lint_files: golangci-lint and (if configured) staticcheck-extra are run
// over the scoped packages, the findings are scoped to the listed files (or
// staged lines when LINT_LINE_RANGES is set), and each gate is reported.
func runLintFiles() int {
	filesText := lintEnvDefault("LINT_FILES", "./...")
	if filesText == "" {
		writeStdout("lint-files: LINT_FILES is empty\n")
		return 0
	}
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	if err := ensureLintTools(); err != nil {
		return statusFromError(err)
	}
	// Resolve (build) the analyzer binary in-process, the work the
	// staticcheck-extra-bin make prerequisite used to do. A resolution failure is
	// best-effort: resolveStaticcheckBin below then reports the gate as skipped.
	_ = staticcheckResolveBin()
	files := splitWords(filesText)
	packages := lint.ScopedPackagesFromFiles(files)
	lineRangesFile := os.Getenv("LINT_LINE_RANGES")
	status := 0

	golangciRaw := filepath.Join(makeDir, "lint-files.golangci.raw.out")
	golangciFlags := lintEnvDefault("GOLANGCI_LINT_RUN_FLAGS", os.Getenv("GOLANGCI_LINT_FLAGS"))
	golangciArgs := append([]string{"run"}, splitWords(golangciFlags)...)
	golangciArgs = append(golangciArgs, packages...)
	if _, err := captureCommand(lintEnvDefault("GOLANGCI_LINT", "golangci-lint"), golangciArgs, golangciRaw); err != nil {
		return statusFromError(err)
	}
	ok, err := runScopedGate(
		"golangci-lint", golangciRaw,
		filepath.Join(makeDir, "lint-files.golangci.out"),
		files,
		lintEnvDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt"),
		golangciExcludePattern(), lineRangesFile,
	)
	if err != nil {
		return statusFromError(err)
	}
	if !ok {
		status = 1
	}

	staticcheckBin := resolveStaticcheckBin()
	if staticcheckBin != "" {
		staticcheckRaw := filepath.Join(makeDir, "lint-files.staticcheck.raw.out")
		staticcheckArgs := append(splitWords(os.Getenv("STATICCHECK_EXTRA_FLAGS")), packages...)
		if _, err := captureCommand(staticcheckBin, staticcheckArgs, staticcheckRaw); err != nil {
			return statusFromError(err)
		}
		excludePattern := lint.ExcludePattern(
			lintEnvDefault("STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
			os.Getenv("STATICCHECK_EXTRA_EXCLUDE_PATHS"),
		)
		ok, err := runScopedGate(
			"staticcheck-extra", staticcheckRaw,
			filepath.Join(makeDir, "lint-files.staticcheck.out"),
			files,
			lintEnvDefault("STATICCHECK_EXTRA_BASELINE", ".staticcheck-extra-baseline.txt"),
			excludePattern, lineRangesFile,
		)
		if err != nil {
			return statusFromError(err)
		}
		if !ok {
			status = 1
		}
	} else {
		writeStdout("staticcheck-extra: not configured (skipped)\n")
	}

	if status != 0 && os.Getenv("BASELINE") != "" {
		writeStdout("\nRun with BASELINE=\"\" to see all findings without the baseline gate.\n")
	}
	return status
}

// resolveStaticcheckBin resolves the staticcheck-extra binary, mirroring the
// shell precedence: an explicit STATICCHECK_EXTRA_BIN, otherwise the
// .make/staticcheck-extra build output when it is executable.
func resolveStaticcheckBin() string {
	if configured := os.Getenv("STATICCHECK_EXTRA_BIN"); configured != "" {
		if isExecutable(configured) {
			return configured
		}
		return ""
	}
	candidate := filepath.Join(makeDir, "staticcheck-extra")
	if isExecutable(candidate) {
		return candidate
	}
	return ""
}

// isExecutable reports whether path exists and has any execute bit set,
// mirroring the shell -x test.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// runScopedGate extracts findings from a raw capture, scopes them to the listed
// files or staged line ranges, and gates them against the baseline, mirroring
// run_scoped_gate. It returns true when the gate passed (including the
// zero-findings short-circuit).
func runScopedGate(gateName, rawPath, findingsPath string, files []string, baselinePath, excludePattern, rangesFile string) (bool, error) {
	extracted, _, err := extractFindings(rawPath, goLocationPattern.String(), excludePattern)
	if err != nil {
		return false, err
	}
	if writeErr := writeFindingsFile(findingsPath, extracted); writeErr != nil {
		return false, writeErr
	}
	scopeLabel := "listed files"
	var filtered []string
	if rangesFile != "" {
		scopeLabel = "staged lines"
		ranges, rangeErr := loadRangeRows(rangesFile)
		if rangeErr != nil {
			return false, rangeErr
		}
		filtered = findings.LineFilter(extracted, ranges)
	} else {
		filtered = lint.FilterScopedFindings(extracted, files)
	}

	if len(filtered) == 0 {
		writeStdout(gateName + ": OK (0 findings on " + scopeLabel + ")\n")
		return true, nil
	}
	if os.Getenv("BASELINE") == "" {
		writeStdout(gateName + " findings on " + scopeLabel + ":\n")
		for _, line := range filtered {
			writeStdout(line + "\n")
		}
		return false, nil
	}
	return runGateAndPrint(
		gateName, filtered, baselinePath,
		"Fix the new findings before this gate will pass.",
		excludePattern, "",
	)
}

// loadRangeRows reads a ranges file of file<TAB>start<TAB>end rows into
// findings.Range values, mirroring the awk linefilter rangefile parse. It reads
// a file, so it emits a boundary log.
func loadRangeRows(path string) ([]findings.Range, error) {
	slog.Info("lint load range rows", slog.String("path", path))
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	ranges := make([]findings.Range, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		start, startErr := atoiSafe(fields[1])
		end, endErr := atoiSafe(fields[2])
		if startErr != nil || endErr != nil {
			continue
		}
		ranges = append(ranges, findings.Range{File: fields[0], Start: start, End: end})
	}
	return ranges, nil
}

// runLintDiff runs the scoped lint against the staged Go files and their staged
// line ranges, mirroring run_lint_diff. It collects the staged file list and a
// unified-diff hunk-range file, then delegates to runLintFiles with BASELINE,
// LINT_FILES, and LINT_LINE_RANGES set.
func runLintDiff() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	stagedFiles, err := stagedGoFiles()
	if err != nil {
		return statusFromError(err)
	}
	if len(stagedFiles) == 0 {
		writeStdout("lint-diff: no staged .go files\n")
		return 0
	}
	patchLines, err := stagedGoDiff()
	if err != nil {
		return statusFromError(err)
	}
	ranges := findings.Ranges(patchLines)
	rangesFile := filepath.Join(makeDir, "lint-diff.ranges")
	if err := writeRangesFile(rangesFile, ranges); err != nil {
		return statusFromError(err)
	}
	if len(ranges) == 0 {
		writeStdout("lint-diff: no staged Go line changes\n")
		return 0
	}
	if os.Getenv("BASELINE") == "" {
		_ = os.Setenv("BASELINE", "1")
	}
	_ = os.Setenv("LINT_FILES", strings.Join(stagedFiles, " "))
	_ = os.Setenv("LINT_LINE_RANGES", rangesFile)
	return runLintFiles()
}

// stagedGoFiles returns the staged added/copied/modified .go files, mirroring
// the git diff --cached --name-only filter in run_lint_diff. It runs git, so it
// emits a boundary log.
func stagedGoFiles() ([]string, error) {
	slog.Info("lint list staged go files")
	out, err := exec.Command("git", "diff", "--cached", "--name-only", "--relative", "--diff-filter=ACM").Output()
	if err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasSuffix(line, ".go") {
			files = append(files, line)
		}
	}
	return files, nil
}

// stagedGoDiff returns the unified-diff lines for staged .go changes with zero
// context, mirroring the git diff --cached --unified=0 in run_lint_diff. It
// runs git, so it emits a boundary log.
func stagedGoDiff() ([]string, error) {
	slog.Info("lint capture staged go diff")
	out, err := exec.Command("git", "diff", "--cached", "--unified=0", "--relative", "--diff-filter=ACM", "--", "*.go").Output()
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(string(out), "\n")
	if text == "" {
		return []string{}, nil
	}
	return strings.Split(text, "\n"), nil
}

// writeRangesFile writes the hunk ranges as file<TAB>start<TAB>end rows,
// mirroring the awk ranges output. It mutates the filesystem, so it emits a
// boundary log.
func writeRangesFile(path string, ranges []findings.Range) error {
	slog.Info("lint write ranges file", slog.String("path", path))
	var builder strings.Builder
	for _, span := range ranges {
		builder.WriteString(span.File)
		builder.WriteString("\t")
		builder.WriteString(itoa(span.Start))
		builder.WriteString("\t")
		builder.WriteString(itoa(span.End))
		builder.WriteString("\n")
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}
