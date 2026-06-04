// The lint chain runner for go-mk, ported from run_lint_chain in
// scripts/go-mk-lint.sh. It runs each gate in LINT_GATES by recursing into
// make (so staticcheck-extra, still a separate script this phase, keeps
// working alongside the ported Go gates), aggregates the per-gate output,
// strips the recursive-make error summary lines, and prints the failure
// summary and the optional bypass banner. This file lives in package main and
// owns process execution and file I/O; boundary functions emit a structured
// slog event.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/gate"
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
// honours the BYPASS_LINT bypass.
func runLintChain() int {
	if logsummary.ParseMode(os.Getenv("GO_MK_LOG")) == logsummary.ModeDebug {
		return runLintChainRaw()
	}
	steps, status, err := collectGateSteps()
	if err != nil {
		return statusFromError(err)
	}
	writeStdout(report.Render(report.Report{
		Title: "lint",
		Steps: steps,
	}))
	if status == 0 {
		return 0
	}
	if bypassPassed() {
		return 0
	}
	return status
}

// collectGateSteps runs each gate as an emit child, decodes its result marker
// into a StepResult, and accumulates the per-gate diagnostic counts. It renders
// nothing so both runLintChain and the build-check orchestrator can reuse it.
func collectGateSteps() ([]report.StepResult, int, error) {
	if err := ensureMakeDir(); err != nil {
		return nil, 0, err
	}
	clearFailedGateFile()
	gateList := splitWords(lintEnvDefault("LINT_GATES", defaultLintGates))
	makeProgram := lintEnvDefault("GO_MK_RECURSIVE_MAKE", lintEnvDefault("MAKE", "make"))
	makeArgs := splitWords(os.Getenv("GO_MK_RECURSIVE_MAKE_ARGS"))

	steps := make([]report.StepResult, 0, len(gateList))
	status := 0
	for _, gateName := range gateList {
		output, gateStatus := runGateViaMake(makeProgram, makeArgs, gateName)
		marker, rest, found := extractMarker(output)
		steps = append(steps, gateStep(gateName, marker, found, gateStatus, rest))
		if gateStatus != 0 {
			status = gateStatus
		}
	}
	return steps, status, nil
}

// runLintChainRaw streams every gate's full output, mirroring the historical
// run_lint_chain. It is the GO_MK_LOG=debug path used to troubleshoot the gates
// without the structured report.
func runLintChainRaw() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	clearFailedGateFile()
	gateList := splitWords(lintEnvDefault("LINT_GATES", defaultLintGates))
	makeProgram := lintEnvDefault("GO_MK_RECURSIVE_MAKE", lintEnvDefault("MAKE", "make"))
	makeArgs := splitWords(os.Getenv("GO_MK_RECURSIVE_MAKE_ARGS"))

	aggregate := make([]string, 0)
	status := 0
	for _, gateName := range gateList {
		writeStdout("lint: running " + gateName + "\n")
		output, gateStatus := runGateViaMake(makeProgram, makeArgs, gateName)
		aggregate = append(aggregate, output...)
		if gateStatus != 0 {
			status = gateStatus
		}
	}
	for _, line := range lint.FilterMakeErrorLines(aggregate) {
		writeStdout(line + "\n")
	}
	if status == 0 {
		return 0
	}
	printFailedSummary(filepath.Join(makeDir, "lint.failed"))
	if bypassPassed() {
		return 0
	}
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

// extractMarker scans captured gate output for the one result marker, returning
// it along with the remaining lines (the marker stripped) and whether a marker
// was found. The remaining lines are kept only as a fallback for a gate that
// failed without emitting a marker.
func extractMarker(lines []string) (report.GateMarker, []string, bool) {
	rest := make([]string, 0, len(lines))
	var marker report.GateMarker
	found := false
	for _, line := range lines {
		if !found {
			if parsed, ok := report.ParseMarker(line); ok {
				marker = parsed
				found = true
				continue
			}
		}
		rest = append(rest, line)
	}
	return marker, rest, found
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

// runGateViaMake runs one gate target via recursive make with GO_MK_SKIP_FETCH
// set, captures combined stdout and stderr into a slice of lines, and returns
// the captured exit status, mirroring the per-gate recurse in run_lint_chain.
// It runs a process, so it emits a boundary log.
func runGateViaMake(makeProgram string, makeArgs []string, gateName string) ([]string, int) {
	slog.Info("lint run gate via make", slog.String("gate", gateName))
	args := make([]string, 0, len(makeArgs)+2)
	args = append(args, makeArgs...)
	args = append(args, "--no-print-directory", gateName)
	cmd := exec.Command(makeProgram, args...)
	childEnv := setEnvVar(os.Environ(), "GO_MK_SKIP_FETCH", "1")
	if logsummary.ParseMode(os.Getenv("GO_MK_LOG")) != logsummary.ModeDebug {
		childEnv = setEnvVar(childEnv, "GO_MK_DIAG_EMIT", "1")
	}
	cmd.Env = childEnv
	out, err := cmd.CombinedOutput()
	lines := splitOutputLines(string(out))
	if err == nil {
		return lines, 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return lines, exitErr.ExitCode()
	}
	slog.Error("lint gate make failed to run", slog.String("gate", gateName), slog.String("err", err.Error()))
	return lines, 1
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

// bypassActive reports whether the BYPASS_LINT token matches the gate token and
// BYPASS_CONFIRM is affirmative. It prints nothing, so both the lint chain and
// the build-check orchestrator can consult it. It runs the token command, so it
// emits a boundary log.
func bypassActive() bool {
	bypassValue := os.Getenv("BYPASS_LINT")
	if lint.Slugify(bypassValue) == "" {
		return false
	}
	if !gate.ConfirmAccepted(os.Getenv("BYPASS_CONFIRM")) {
		return false
	}
	slog.Info("lint evaluate bypass token")
	expectedRaw, ok := gateTokenExpected(os.Getenv("BYPASS_TOKEN_CMD"))
	if !ok {
		return false
	}
	return gate.TokensMatch(expectedRaw, bypassValue)
}

// bypassPassed reports whether the lint bypass is active, printing the bypass
// banner when it is, mirroring the bypass arm of run_lint_chain. The banner
// names BYPASS_LINT but never prints the matched token value.
func bypassPassed() bool {
	if !bypassActive() {
		return false
	}
	writeStdout("\n***********************************************************************\n")
	writeStdout("*** LINT FINDINGS NON-BLOCKING via BYPASS_LINT\n")
	writeStdout("*** Findings reported above but build proceeds. Do not merge without fixing.\n")
	writeStdout("***********************************************************************\n\n")
	return true
}

// atoiSafe parses an integer, returning an error for non-numeric input.
func atoiSafe(text string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(text))
}

// itoa renders an integer as its decimal string.
func itoa(value int) string {
	return strconv.Itoa(value)
}
