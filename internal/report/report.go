// Package report renders the single, clean run report the go-mk monolith prints
// for a lint or build-check run. The command layer collects one StepResult per
// check (gate, vet, govulncheck), and this package turns them into one scannable
// block: an aligned status table, the findings shown once under each failing
// check, and a single verdict line. It is pure string formatting with no I/O, so
// it is fully unit tested and never decides pass or fail; the caller supplies the
// status it computed from the gate.
package report

import (
	"fmt"
	"strings"
)

// Status is a check's outcome. The report never computes it; the caller sets it
// from the gate's own verdict so detection logic stays in the gate.
type Status int

const (
	// StatusOK marks a passing check.
	StatusOK Status = iota
	// StatusFailed marks a failing check whose findings are shown in full.
	StatusFailed
)

// StepResult is one check's outcome. Findings holds the pre-formatted display
// lines (for example findings.Print output split into lines) shown only on a
// failure; Note is the short parenthetical after FAILED such as "1 new finding";
// Remediation is the caller's fix hint printed under the findings.
type StepResult struct {
	Name        string
	Status      Status
	Note        string
	Findings    []string
	Remediation string
}

// Report is the whole run.
type Report struct {
	Title string
	Steps []StepResult
}

func (step StepResult) failed() bool {
	return step.Status == StatusFailed
}

// Render returns the one clean report as a single string ending in a newline.
func Render(rep Report) string {
	var builder strings.Builder
	if rep.Title != "" {
		builder.WriteString(rep.Title)
		builder.WriteString("\n\n")
	}

	nameWidth := 0
	for _, step := range rep.Steps {
		if len(step.Name) > nameWidth {
			nameWidth = len(step.Name)
		}
	}

	failedNames := make([]string, 0, len(rep.Steps))
	for _, step := range rep.Steps {
		fmt.Fprintf(&builder, "  %-*s  %s\n", nameWidth, step.Name, statusLabel(step))
		if step.failed() {
			failedNames = append(failedNames, step.Name)
		}
	}

	for _, step := range rep.Steps {
		if !step.failed() || len(step.Findings) == 0 {
			continue
		}
		builder.WriteString("\n  ")
		builder.WriteString(step.Name)
		builder.WriteString("\n")
		for _, line := range step.Findings {
			builder.WriteString("  ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
		if step.Remediation != "" {
			builder.WriteString("    Fix: ")
			builder.WriteString(step.Remediation)
			builder.WriteString("\n")
		}
	}

	builder.WriteString("\n")
	if len(failedNames) == 0 {
		builder.WriteString("  All checks passed.\n")
		return builder.String()
	}
	fmt.Fprintf(&builder, "  %d check%s failed: %s\n",
		len(failedNames), plural(len(failedNames)), strings.Join(failedNames, ", "))
	return builder.String()
}

// statusLabel renders one step's status cell: "ok", or "FAILED" with the
// optional parenthetical note.
func statusLabel(step StepResult) string {
	if !step.failed() {
		return "ok"
	}
	if step.Note == "" {
		return "FAILED"
	}
	return "FAILED  (" + step.Note + ")"
}

// plural returns "s" for any count other than one.
func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
