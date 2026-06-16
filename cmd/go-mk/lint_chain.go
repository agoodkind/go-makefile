// The lint chain runner for go-mk, ported from run_lint_chain in
// scripts/go-mk-lint.sh. It runs each gate in LINT_GATES by recursing into
// make (so staticcheck-extra, still a separate script this phase, keeps
// working alongside the ported Go gates), aggregates the per-gate output,
// strips the recursive-make error summary lines, and prints the failure
// summary. This file lives in package main and owns process execution and file
// I/O; boundary functions emit a structured slog event.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
	"goodkind.io/go-makefile/internal/logsummary"
	"goodkind.io/go-makefile/internal/report"
)

// defaultLintGates is the gate list LINT_GATES overrides.
const defaultLintGates = "lint-golangci lint-format lint-gocyclo lint-deadcode staticcheck-extra"

// runLintChain runs every gate in LINT_GATES and prints one clean report. In the
// default and quiet modes each gate runs as an emit child whose structured
// marker the parent collects into a single report.Render; GO_MK_LOG=debug keeps
// the historical raw streaming for troubleshooting. It returns the exit code and
// fails when any gate fails.
func runLintChain() int {
	// notice runs in-process here, the work the go-mk-notice make prerequisite
	// used to do. It is best-effort and prints only to stderr.
	runNotice()
	if logsummary.ParseMode(os.Getenv("GO_MK_LOG")) == logsummary.ModeDebug {
		return runLintChainRaw()
	}
	if err := prepareChecks(false); err != nil {
		return statusFromError(err)
	}
	status := runChecks("lint", gateChecks())
	if status == 0 {
		return 0
	}
	return status
}

// gateRunners maps each LINT_GATES name to its in-process runner. LINT_GATES
// still selects and orders the gates; the chain invokes these directly instead
// of recursing into make, so the lint run is one go-mk process under one trace.
// A gate name absent from this map is reported as a failed step rather than
// silently passing.
func gateRunners() map[string]func() int {
	return map[string]func() int{
		"lint-golangci":     runLintGolangci,
		"lint-format":       runLintFormat,
		"lint-gocyclo":      runLintGocyclo,
		"lint-deadcode":     runLintDeadcode,
		"staticcheck-extra": runStaticcheckExtra,
	}
}

// unknownGateStep records and renders a LINT_GATES entry that has no in-process
// runner, so a misconfigured gate list is visible instead of silently skipped.
func unknownGateStep(gateName string) report.StepResult {
	slog.Warn("lint encountered unknown gate", slog.String("gate", gateName))
	return report.StepResult{
		Name:     gateName,
		Status:   report.StatusFailed,
		Findings: []string{"unknown lint gate; not registered for in-process execution"},
	}
}

// runLintChainRaw streams every gate's full output for the GO_MK_LOG=debug path
// used to troubleshoot the gates without the structured report. It ensures the
// lint tools, then runs each gate in-process in render mode (GO_MK_DIAG_EMIT
// unset) so the gate prints its own human text directly.
func runLintChainRaw() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	clearFailedGateFile()
	if err := runLintTools(); err != nil {
		return statusFromError(err)
	}
	runners := gateRunners()
	gateList := splitWords(lintEnvDefault("LINT_GATES", defaultLintGates))

	status := 0
	for _, gateName := range gateList {
		writeStdout("lint: running " + gateName + "\n")
		runner, ok := runners[gateName]
		if !ok {
			writeStdout("lint: unknown gate " + gateName + "\n")
			status = 1
			continue
		}
		if code := runner(); code != 0 {
			status = code
		}
	}
	if status == 0 {
		return 0
	}
	printFailedSummary(filepath.Join(makeDir, "lint.failed"))
	return status
}

// clearFailedGateFile removes the per-gate failure record so a fresh run starts
// clean, mirroring the shell rm -f. A missing file is not an error.
func clearFailedGateFile() {
	failedPath := filepath.Join(makeDir, "lint.failed")
	if err := os.Remove(failedPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("lint could not clear failed-gate file", slog.String("err", err.Error()))
	}
}

// gateStep turns one gate's marker (or, as a fallback, its exit status and
// captured output) into a StepResult. The gate's detection is untouched: a
// failing gate's findings come straight from the marker, formatted for display.
func gateStep(gateName string, marker report.GateMarker, found bool, status int, rest []string) report.StepResult {
	if found {
		if marker.Passed {
			return report.StepResult{Name: gateName, Status: report.StatusOK}
		}
		return report.StepResult{
			Name:        gateName,
			Status:      report.StatusFailed,
			Note:        fmt.Sprintf("%d new finding%s", len(marker.Findings), findingPlural(len(marker.Findings))),
			Findings:    formatFindings(marker.Findings),
			Remediation: marker.Remediation,
		}
	}
	if status == 0 {
		return report.StepResult{Name: gateName, Status: report.StatusOK}
	}
	return report.StepResult{
		Name:     gateName,
		Status:   report.StatusFailed,
		Findings: lint.FilterMakeErrorLines(rest),
	}
}

// formatFindings renders each raw finding through findings.Print and splits the
// result into display lines the report indents under the failing gate.
func formatFindings(raw []string) []string {
	out := make([]string, 0, len(raw)*2)
	for _, finding := range raw {
		rendered := strings.TrimSuffix(findings.Print(finding, "", ""), "\n")
		out = append(out, strings.Split(rendered, "\n")...)
	}
	return out
}

// findingPlural returns "s" for any count other than one.
func findingPlural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// splitOutputLines splits captured output into lines, dropping a single
// trailing empty element from the final newline so the aggregate has one
// element per output line.
func splitOutputLines(text string) []string {
	if text == "" {
		return []string{}
	}
	trimmed := strings.TrimSuffix(text, "\n")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "\n")
}

// printFailedSummary prints the "lint: FAILED" block, listing the failed gate
// names from .make/lint.failed when present, mirroring the shell summary. It
// reads the file, so it emits a boundary log via readFileLines.
func printFailedSummary(failedPath string) {
	lines, err := readFileLines(failedPath)
	if err == nil && len(lines) > 0 {
		failed := lint.DedupeFailedGates(lines)
		writeStdout("\nlint: FAILED\n")
		writeStdout("  Failed gates: " + strings.Join(failed, ", ") + "\n")
		return
	}
	writeStdout("\nlint: FAILED\n")
	writeStdout("  Failed gates: see failed target output above\n")
}

// atoiSafe parses an integer, returning an error for non-numeric input.
func atoiSafe(text string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(text))
}

// itoa renders an integer as its decimal string.
func itoa(value int) string {
	return strconv.Itoa(value)
}
