// Package report renders the single, clean run report the go-mk monolith prints
// for a lint or build-check run. The command layer collects one StepResult per
// check (gate, vet, govulncheck), and this package turns them into one scannable
// block: an aligned status table, the findings shown once under each failing
// check, and a single verdict line. It is pure string formatting with no I/O, so
// it is fully unit tested and never decides pass or fail; the caller supplies the
// status it computed from the gate.
//
// Render composes the same exported helpers (NameWidth, Row, StepRow,
// FindingsBlock, Footer) that the live progress display and the non-TTY streamer
// reuse, so a streamed run and a batch run emit byte-identical text.
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

// GateMarker is one lint gate's verdict as the command layer hands it back: the
// gate's own pass/fail, its new findings, and its remediation hint. The command
// layer turns it into a StepResult; the gate's detection stays untouched.
type GateMarker struct {
	Name        string
	Passed      bool
	Findings    []string
	Remediation string
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

	nameWidth := NameWidth(rep.Steps)

	failedNames := make([]string, 0, len(rep.Steps))
	for _, step := range rep.Steps {
		builder.WriteString(StepRow(nameWidth, step))
		builder.WriteString("\n")
		if step.failed() {
			failedNames = append(failedNames, step.Name)
		}
	}

	for _, step := range rep.Steps {
		builder.WriteString(FindingsBlock(step))
	}

	builder.WriteString(Footer(failedNames))
	return builder.String()
}

// NameWidth returns the column width for the status table: the longest step
// name. The streamer precomputes this from the full step list so the first
// streamed row aligns with the last.
func NameWidth(steps []StepResult) int {
	width := 0
	for _, step := range steps {
		if len(step.Name) > width {
			width = len(step.Name)
		}
	}
	return width
}

// Row formats one status-table line without a trailing newline: two leading
// spaces, the name left-padded to width, two spaces, then the status cell. The
// live display passes a spinner frame or a pending marker as the cell; the batch
// and streaming paths pass the resolved status label.
func Row(width int, name, statusCell string) string {
	return fmt.Sprintf("  %-*s  %s", width, name, statusCell)
}

// StepRow formats a resolved step's status-table line without a trailing
// newline, using the step's own status label as the cell.
func StepRow(width int, step StepResult) string {
	return Row(width, step.Name, StatusLabel(step))
}

// FindingsBlock returns the per-failure detail block for one step, or the empty
// string when the step passed or carries no findings. The block leads with a
// blank line so successive blocks stay separated, matching the batch report.
func FindingsBlock(step StepResult) string {
	if !step.failed() || len(step.Findings) == 0 {
		return ""
	}
	var builder strings.Builder
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
	return builder.String()
}

// Footer returns the trailing blank line and the single verdict line for the
// given failed step names: the all-clear line when none failed, otherwise the
// count and the comma-joined names.
func Footer(failedNames []string) string {
	if len(failedNames) == 0 {
		return "\n  All checks passed.\n"
	}
	return fmt.Sprintf("\n  %d check%s failed: %s\n",
		len(failedNames), plural(len(failedNames)), strings.Join(failedNames, ", "))
}

// StatusLabel renders one step's status cell: "ok", or "FAILED" with the
// optional parenthetical note.
func StatusLabel(step StepResult) string {
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
