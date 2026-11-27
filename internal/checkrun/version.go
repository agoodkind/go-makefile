package checkrun

import (
	"fmt"
	"log/slog"

	"goodkind.io/go-makefile/internal/report"
)

const (
	// GoVersionTitle identifies the standalone Go version report.
	GoVersionTitle = "go-mk go-version-check"

	goVersionCheck         = "go-version"
	goVersionBehindMessage = "go version behind latest stable"
	goVersionBehindFinding = "Installed Go %s is behind the latest stable Go %s."
	goVersionUpgradeNotice = "Update the installed Go toolchain when practical."
)

// GoVersion compares the installed Go version with the latest stable release.
//
// Check policy determines the status for an outdated version or a lookup error.
//
//	result := GoVersion(installed, latest, compare)
func GoVersion(
	installed func() (string, error),
	latest func() (string, error),
	compare func(string, string) int,
) report.StepResult {
	installedVersion, err := installed()
	if err != nil {
		return Diagnostic(goVersionCheck, err.Error())
	}

	latestVersion, err := latest()
	if err != nil {
		return Diagnostic(goVersionCheck, err.Error())
	}

	if compare(installedVersion, latestVersion) >= 0 {
		return report.StepResult{
			Name:        goVersionCheck,
			Status:      report.StatusOK,
			Note:        "",
			Findings:    nil,
			Remediation: "",
		}
	}

	slog.Warn(goVersionBehindMessage)
	findings := []string{
		fmt.Sprintf(goVersionBehindFinding, installedVersion, latestVersion),
		goVersionUpgradeNotice,
	}

	return Diagnostic(goVersionCheck, findings...)
}
