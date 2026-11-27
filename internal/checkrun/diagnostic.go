package checkrun

import (
	"goodkind.io/go-makefile/config"
	"goodkind.io/go-makefile/internal/report"
)

// Diagnostic reports a tool finding or execution error.
//
// Check policy determines whether the result blocks the build.
//
//	result := Diagnostic("go-version", "lookup failed")
func Diagnostic(name string, findings ...string) report.StepResult {
	status := report.StatusFailed
	if config.AdvisoryCheck(name) {
		status = report.StatusAdvisory
	}

	return report.StepResult{
		Name:        name,
		Status:      status,
		Note:        "",
		Findings:    findings,
		Remediation: "",
	}
}

// Outcome pairs a check result with its exit status.
//
// A failed check returns status 1. An advisory returns status 0.
//
// Example:
//
//	report, status := Outcome(result)
func Outcome(result report.StepResult) (report.StepResult, int) {
	return result, exitStatus(result)
}

func exitStatus(result report.StepResult) int {
	if result.Status == report.StatusFailed {
		return 1
	}

	return 0
}

// PrintStep reports a standalone check and returns its exit status.
//
// The report includes any findings before the command exits.
//
//	status := PrintStep("go-mk vet", result, stdout)
func PrintStep(title string, result report.StepResult, stdout func(string)) int {
	stdout(
		report.Render(report.Report{
			Title: title,
			Steps: []report.StepResult{result},
		}),
	)

	return exitStatus(result)
}
