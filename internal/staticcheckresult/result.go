// Package staticcheckresult separates incomplete analysis from observed
// diagnostics.
package staticcheckresult

import (
	"regexp"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
)

// DiagnosticsStatus identifies a completed run that reported lint diagnostics.
const DiagnosticsStatus = 3

var (
	analyzerErrorLine = regexp.MustCompile(`^[[:alpha:]_][[:alnum:]_]*: `)
	cascadeLine       = regexp.MustCompile(
		`^[[:alpha:]_][[:alnum:]_]*: (analysis skipped due to errors in package|failed prerequisites: )`,
	)
)

// Result separates observed findings from package and analyzer failures.
type Result struct {
	Status        int
	Findings      []string
	PackageErrors []string
	ToolErrors    []string
}

// Parse classifies the plain output of the staticcheck-extra analysis driver.
//
// The driver prints package errors, then analyzer errors, then located
// diagnostics. An incomplete run can report findings from successful analyzers.
//
// A partial run retains both kinds of output:
//
//	result := Parse(1, lines)
func Parse(status int, lines []string) Result {
	result := Result{Status: status, Findings: nil, PackageErrors: nil, ToolErrors: nil}
	if result.Complete() {
		result.Findings = uniqueLines(lines)

		return result
	}

	firstError, lastError := -1, -1

	for index, line := range lines {
		if !analyzerErrorLine.MatchString(line) {
			continue
		}

		if firstError < 0 {
			firstError = index
		}

		lastError = index
	}

	if firstError < 0 {
		result.ToolErrors = uniqueLines(lines)
		for _, line := range lines {
			if _, located := findings.FindingPath(line); located {
				result.PackageErrors = result.ToolErrors
				result.ToolErrors = nil

				break
			}
		}

		return result
	}

	result.PackageErrors = uniqueLines(lines[:firstError])

	result.ToolErrors = uniqueLines(lines[firstError : lastError+1])
	for _, line := range uniqueLines(lines[lastError+1:]) {
		if _, located := findings.FindingPath(line); located {
			result.Findings = append(result.Findings, line)

			continue
		}

		result.ToolErrors = append(result.ToolErrors, line)
	}

	result.ToolErrors = uniqueLines(result.ToolErrors)

	return result
}

// Complete reports whether the driver finished its selected analysis.
//
// Status 3 reports completed analysis with lint findings.
//
// A successful analysis satisfies:
//
//	Parse(0, nil).Complete()
func (result Result) Complete() bool {
	return result.Status == 0 || result.Status == DiagnosticsStatus
}

// FailureLines renders analysis failures without relabeling lint diagnostics.
//
// Compiler and package-loading errors remain separate from analyzer failures.
//
// A caller prints the returned lines after comparing observed findings:
//
//	lines := result.FailureLines()
func (result Result) FailureLines() []string {
	if result.Complete() {
		return nil
	}

	lines := []string{
		"The staticcheck-extra analysis did not complete (exit status " +
			strconv.Itoa(result.Status) + ").",
	}
	if len(result.PackageErrors) > 0 {
		lines = append(lines, "Package loading or compiler errors:")
		lines = append(lines, result.PackageErrors...)
	}

	if len(result.ToolErrors) > 0 {
		lines = append(lines, "Analyzer or tool errors:")
		lines = append(lines, result.ToolErrors...)
	}

	return lines
}

// IncompleteError prevents partial results from updating historical baselines.
type IncompleteError struct {
	Result Result
}

// Error renders the failures that prevented complete analysis.
//
// Observed lint diagnostics remain separate for baseline comparison.
//
// A caller can report the capture error with:
//
//	message := failure.Error()
func (failure *IncompleteError) Error() string {
	return strings.Join(failure.Result.FailureLines(), "\n")
}

func uniqueLines(lines []string) []string {
	seen := make(map[string]bool)

	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || cascadeLine.MatchString(line) || seen[line] {
			continue
		}

		seen[line] = true
		result = append(result, line)
	}

	return result
}
