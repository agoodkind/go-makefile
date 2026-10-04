// Package mutation shapes the results of a mutation test run. The command layer
// in cmd/go-mk runs the mutation tool once per package directory and reads each
// JSON report. This package parses a report, totals the counts, computes the
// score, and renders the summary. It reads no file and writes to no process
// stream.
package mutation

import (
	"encoding/json"
	"fmt"
	"strings"
)

const percentScale = 100

// Report is the subset of one gremlins JSON report that the summary uses. Files
// keeps the per-file mutant list verbatim for the combined report.
type Report struct {
	MutantsTotal      int             `json:"mutants_total"`
	MutantsKilled     int             `json:"mutants_killed"`
	MutantsLived      int             `json:"mutants_lived"`
	MutantsNotViable  int             `json:"mutants_not_viable"`
	MutantsNotCovered int             `json:"mutants_not_covered"`
	MutantsTimedOut   int             `json:"mutants_timed_out"`
	ElapsedTime       float64         `json:"elapsed_time"`
	Files             json.RawMessage `json:"files,omitempty"`
}

// Gremlins reports no total for this status.
const timedOutStatus = "TIMED OUT"

type reportFile struct {
	Mutations []reportMutation `json:"mutations"`
}

type reportMutation struct {
	Status string `json:"status"`
}

// PackageReport is the report of one package directory.
type PackageReport struct {
	Package string `json:"package"`
	Report
}

// Summary totals the reports of one run.
type Summary struct {
	Packages   []PackageReport `json:"packages"`
	Killed     int             `json:"mutants_killed"`
	Lived      int             `json:"mutants_lived"`
	NotCovered int             `json:"mutants_not_covered"`
	NotViable  int             `json:"mutants_not_viable"`
	TimedOut   int             `json:"mutants_timed_out"`
	Total      int             `json:"mutants_total"`
}

// ParseReport decodes one gremlins JSON report and counts the timed-out mutants
// from the per-file mutant list.
func ParseReport(data []byte) (Report, error) {
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, err
	}
	if len(report.Files) == 0 {
		return report, nil
	}
	var files []reportFile
	if err := json.Unmarshal(report.Files, &files); err != nil {
		return Report{}, err
	}
	report.MutantsTimedOut = 0
	for _, file := range files {
		for _, mutant := range file.Mutations {
			if mutant.Status == timedOutStatus {
				report.MutantsTimedOut++
			}
		}
	}
	return report, nil
}

// Summarize totals the package reports in order.
func Summarize(reports []PackageReport) Summary {
	summary := Summary{Packages: reports}
	for _, report := range reports {
		summary.Killed += report.MutantsKilled
		summary.Lived += report.MutantsLived
		summary.NotCovered += report.MutantsNotCovered
		summary.NotViable += report.MutantsNotViable
		summary.TimedOut += report.MutantsTimedOut
		summary.Total += report.MutantsTotal
	}
	return summary
}

// Score returns the percentage of covered mutants that the tests killed:
// killed / (killed + lived). The second result is false when the run has no
// covered mutant, and the score is then undefined.
func (summary Summary) Score() (float64, bool) {
	return score(summary.Killed, summary.Lived)
}

func score(killed, lived int) (float64, bool) {
	covered := killed + lived
	if covered == 0 {
		return 0, false
	}
	return float64(killed) / float64(covered) * percentScale, true
}

// MeetsMinimum reports whether the run satisfies the minimum score. A minimum of
// zero or less is always met. A run with an undefined score fails a positive
// minimum.
func (summary Summary) MeetsMinimum(minimum float64) bool {
	if minimum <= 0 {
		return true
	}
	value, defined := summary.Score()
	return defined && value >= minimum
}

// RenderMarkdown renders the summary as a Markdown table with one row per
// package, a total row, and the minimum score verdict.
func RenderMarkdown(summary Summary, minimum float64) string {
	var builder strings.Builder
	builder.WriteString("## Mutation test summary\n\n")
	builder.WriteString("| Package | Killed | Lived | Not covered | Timed out | Not viable | Score |\n")
	builder.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, report := range summary.Packages {
		builder.WriteString(tableRow(
			report.Package, report.MutantsKilled, report.MutantsLived,
			report.MutantsNotCovered, report.MutantsTimedOut, report.MutantsNotViable,
		))
	}
	builder.WriteString(tableRow(
		"Total", summary.Killed, summary.Lived, summary.NotCovered, summary.TimedOut, summary.NotViable,
	))
	builder.WriteString("\nThe score is killed / (killed + lived).\n")
	if summary.TimedOut > 0 {
		fmt.Fprintf(
			&builder,
			"%d timed-out mutants do not count toward the score. Raise MUTATION_TIMEOUT_COEFFICIENT and run again.\n",
			summary.TimedOut,
		)
	}
	builder.WriteString(minimumLine(summary, minimum))
	return builder.String()
}

func tableRow(name string, killed, lived, notCovered, timedOut, notViable int) string {
	return fmt.Sprintf(
		"| %s | %d | %d | %d | %d | %d | %s |\n",
		name, killed, lived, notCovered, timedOut, notViable, scoreText(killed, lived),
	)
}

func scoreText(killed, lived int) string {
	value, defined := score(killed, lived)
	if !defined {
		return "n/a"
	}
	return fmt.Sprintf("%.2f%%", value)
}

func minimumLine(summary Summary, minimum float64) string {
	if minimum <= 0 {
		return "No minimum score is set.\n"
	}
	if summary.MeetsMinimum(minimum) {
		return fmt.Sprintf("The run meets the minimum score of %.2f%%.\n", minimum)
	}
	return fmt.Sprintf("The run is below the minimum score of %.2f%%.\n", minimum)
}
