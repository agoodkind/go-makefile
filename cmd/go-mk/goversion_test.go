package main

import (
	"errors"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/report"
)

func TestOutdatedGoVersionIsAdvisory(t *testing.T) {
	result := goVersionStepWith(goVersionConfig{
		moduleVersion: func() (string, error) {
			return "1.26.4", nil
		},
		latestVersion: func() (string, error) {
			return "1.26.5", nil
		},
	})

	if result.Status != report.StatusAdvisory {
		t.Fatalf("status = %v, want advisory", result.Status)
	}
	if !strings.Contains(strings.Join(result.Findings, "\n"), "behind the latest stable Go 1.26.5") {
		t.Fatalf("findings = %v, want upgrade notice", result.Findings)
	}
}

func TestGoVersionLookupFailureIsAdvisory(t *testing.T) {
	result := goVersionStepWith(goVersionConfig{
		moduleVersion: func() (string, error) {
			return "1.26.5", nil
		},
		latestVersion: func() (string, error) {
			return "", errors.New("network unavailable")
		},
	})

	if result.Status != report.StatusAdvisory {
		t.Fatalf("status = %v, want advisory", result.Status)
	}
	if !strings.Contains(strings.Join(result.Findings, "\n"), "network unavailable") {
		t.Fatalf("findings = %v, want lookup error", result.Findings)
	}
}

func TestCurrentGoVersionIsOK(t *testing.T) {
	result := goVersionStepWith(goVersionConfig{
		moduleVersion: func() (string, error) {
			return "1.26.5", nil
		},
		latestVersion: func() (string, error) {
			return "1.26.5", nil
		},
	})

	if result.Status != report.StatusOK {
		t.Fatalf("status = %v, want ok", result.Status)
	}
}
