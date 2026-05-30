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
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/lint"
)

// runLintChain runs every gate in LINT_GATES via recursive make, aggregates
// the output, strips make error lines, and prints the failure summary. It
// returns the process exit code. It mirrors run_lint_chain, including the
// "lint: running <gate>" lines, the make-error-line filtering, the "lint:
// FAILED" / "Failed gates:" summary, and the BYPASS_LINT bypass.
func runLintChain() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	failedPath := filepath.Join(makeDir, "lint.failed")
	if err := os.Remove(failedPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("lint could not clear failed-gate file", slog.String("err", err.Error()))
	}

	gateList := splitWords(lintEnvDefault("LINT_GATES", "lint-golangci lint-format lint-gocyclo lint-deadcode staticcheck-extra"))
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

	printFailedSummary(failedPath)

	if bypassPassed() {
		return 0
	}
	return status
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
	cmd.Env = setEnvVar(os.Environ(), "GO_MK_SKIP_FETCH", "1")
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

// bypassPassed reports whether the BYPASS_LINT token matches the gate token and
// BYPASS_CONFIRM is set, in which case the chain runner prints the bypass
// banner and returns success, mirroring the bypass arm of run_lint_chain. It
// runs the token command, so it emits a boundary log.
func bypassPassed() bool {
	bypassValue := lint.Slugify(os.Getenv("BYPASS_LINT"))
	if bypassValue == "" {
		return false
	}
	tokenCmd := lintEnvDefault("BYPASS_TOKEN_CMD", os.Getenv("GO_MK_GATE_TOKEN_CMD"))
	if tokenCmd == "" {
		return false
	}
	slog.Info("lint evaluate bypass token")
	out, err := exec.Command("sh", "-c", tokenCmd).Output()
	if err != nil {
		return false
	}
	expected := lint.Slugify(string(out))
	if expected == "" || bypassValue != expected || os.Getenv("BYPASS_CONFIRM") != "1" {
		return false
	}
	writeStdout("\n***********************************************************************\n")
	writeStdout("*** LINT FINDINGS NON-BLOCKING via BYPASS_LINT=" + expected + "\n")
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
