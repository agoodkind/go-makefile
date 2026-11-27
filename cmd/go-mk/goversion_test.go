package main

import (
	"errors"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/checkrun"
	"goodkind.io/go-makefile/internal/report"
)

const (
	outdatedGoVersion     = "1.26.4"
	currentGoVersion      = "1.26.5"
	goVersionFinding      = "Installed Go " + outdatedGoVersion + " is behind the latest stable Go " + currentGoVersion
	goVersionLookupError  = "network unavailable"
	goVersionFindingBreak = "\n"
	goVersionWantAdvisory = "status = %v, want advisory"
	goVersionWantOK       = "status = %v, want ok"
	goVersionWantUpgrade  = "findings = %v, want upgrade notice"
	goVersionWantError    = "findings = %v, want lookup error"
)

func TestOutdatedInstalledGoVersionIsAdvisory(t *testing.T) {
	result := checkrun.GoVersion(
		func() (string, error) {
			return outdatedGoVersion, nil
		},
		func() (string, error) {
			return currentGoVersion, nil
		},
		compareGoVersions,
	)

	if result.Status != report.StatusAdvisory {
		t.Fatalf(goVersionWantAdvisory, result.Status)
	}

	if !strings.Contains(strings.Join(result.Findings, goVersionFindingBreak), goVersionFinding) {
		t.Fatalf(goVersionWantUpgrade, result.Findings)
	}
}

func TestGoVersionLookupFailureIsAdvisory(t *testing.T) {
	result := checkrun.GoVersion(
		func() (string, error) {
			return currentGoVersion, nil
		},
		func() (string, error) {
			return "", errors.New(goVersionLookupError)
		},
		compareGoVersions,
	)

	if result.Status != report.StatusAdvisory {
		t.Fatalf(goVersionWantAdvisory, result.Status)
	}

	if !strings.Contains(
		strings.Join(result.Findings, goVersionFindingBreak),
		goVersionLookupError,
	) {
		t.Fatalf(goVersionWantError, result.Findings)
	}
}

func TestCurrentGoVersionIsOK(t *testing.T) {
	result := checkrun.GoVersion(
		func() (string, error) {
			return currentGoVersion, nil
		},
		func() (string, error) {
			return currentGoVersion, nil
		},
		compareGoVersions,
	)

	if result.Status != report.StatusOK {
		t.Fatalf(goVersionWantOK, result.Status)
	}
}
