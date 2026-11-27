package checkrun

import (
	"fmt"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
	"goodkind.io/go-makefile/internal/report"
)

// GateStep reports the outcome of a lint gate.
//
// A passing marker with findings displays those findings as advisory.
//
//	step := GateStep(name, marker, found, exit, output)
func GateStep(
	gateName string,
	marker report.GateMarker,
	found bool,
	status int,
	rest []string,
) report.StepResult {
	if found {
		if marker.Passed && len(marker.Findings) == 0 {
			return report.StepResult{Name: gateName, Status: report.StatusOK}
		}

		resultStatus := report.StatusFailed
		remediation := marker.Remediation
		if marker.Passed {
			remediation = ""
		}

		if marker.Passed || gateName == "lint-deadcode" {
			resultStatus = report.StatusAdvisory
		}

		return report.StepResult{
			Name:   gateName,
			Status: resultStatus,
			Note: fmt.Sprintf(
				"%d new finding%s",
				len(marker.Findings),
				findingPlural(len(marker.Findings)),
			),
			Findings:    formatFindings(marker.Findings),
			Remediation: remediation,
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

func formatFindings(raw []string) []string {
	out := make([]string, 0, len(raw)*2)
	for _, finding := range raw {
		rendered := strings.TrimSuffix(findings.Print(finding, "", ""), "\n")
		out = append(out, strings.Split(rendered, "\n")...)
	}

	return out
}

func findingPlural(count int) string {
	if count == 1 {
		return ""
	}

	return "s"
}
