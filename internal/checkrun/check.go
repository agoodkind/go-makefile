// Package checkrun executes checks and renders their results.
package checkrun

import (
	"os"

	"golang.org/x/term"

	"goodkind.io/go-makefile/internal/report"
)

// Check defines one named operation and its result function.
type Check struct {
	Name string
	Run  func() (report.StepResult, int)
}

// Run executes checks and renders their progress.
//
// Run enables gate collection during execution and chooses terminal or
// streamed output.
//
//	status := Run("lint", checks, &collecting, stdout)
func Run(title string, checks []Check, collecting *bool, stdout func(string)) int {
	*collecting = true
	defer func() { *collecting = false }()
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return runChecksTUI(title, checks, stdout)
	}

	return Stream(title, checks, stdout)
}

// Stream prints each check result as its operation finishes.
//
// Findings and the verdict follow the status rows in report.Render order.
//
//	status := Stream("lint", checks, stdout)
func Stream(title string, checks []Check, stdout func(string)) int {
	stdout(title + "\n\n")
	width := NameWidth(checks)
	results := make([]report.StepResult, len(checks))

	status := Execute(
		checks,
		func(index int, result report.StepResult) {
			results[index] = result
			stdout(report.StepRow(width, result) + "\n")
		},
	)
	for _, result := range results {
		stdout(report.FindingsBlock(result))
	}

	stdout(report.Footer(FailedNames(results), AdvisoryCount(results)))

	return status
}

// Execute runs checks in order and reports each completed result.
//
// The return value contains the last nonzero status. The callback must not
// be nil.
//
//	status := Execute(checks, onDone)
func Execute(checks []Check, onDone func(int, report.StepResult)) int {
	status := 0

	for index, current := range checks {
		result, code := current.Run()
		if code != 0 {
			status = code
		}

		onDone(index, result)
	}

	return status
}

// NameWidth calculates the status table column width.
//
// The calculation uses every check name before execution starts.
//
//	width := NameWidth(checks)
func NameWidth(checks []Check) int {
	steps := make([]report.StepResult, len(checks))
	for index, current := range checks {
		steps[index] = report.StepResult{
			Name:        current.Name,
			Status:      report.StatusOK,
			Note:        "",
			Findings:    nil,
			Remediation: "",
		}
	}

	return report.NameWidth(steps)
}

// FailedNames returns failed check names in result order.
//
// The report footer uses these names to identify failed checks.
//
//	failed := FailedNames(results)
func FailedNames(results []report.StepResult) []string {
	failed := make([]string, 0, len(results))
	for _, result := range results {
		if result.Status == report.StatusFailed {
			failed = append(failed, result.Name)
		}
	}

	return failed
}

// AdvisoryCount counts results with advisory status.
//
// The report footer displays this count separately from failed checks.
//
//	count := AdvisoryCount(results)
func AdvisoryCount(results []report.StepResult) int {
	count := 0

	for _, result := range results {
		if result.Status == report.StatusAdvisory {
			count++
		}
	}

	return count
}
