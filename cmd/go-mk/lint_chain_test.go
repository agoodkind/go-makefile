package main

import (
	"testing"

	"goodkind.io/go-makefile/internal/report"
)

func TestGateStepFromMarker(t *testing.T) {
	pass := gateStep("lint-format", report.GateMarker{Name: "lint-format", Passed: true}, true, 0, nil)
	if pass.Status != report.StatusOK {
		t.Errorf("passing gate should be ok, got %+v", pass)
	}

	fail := gateStep("lint-gocyclo", report.GateMarker{
		Name:        "lint-gocyclo",
		Passed:      false,
		Findings:    []string{"a.go:42:1: gocyclo: too complex"},
		Remediation: "refresh the baseline.",
	}, true, 1, nil)
	if fail.Status != report.StatusFailed {
		t.Fatalf("failing gate should be failed, got %+v", fail)
	}
	if fail.Note != "1 new finding" {
		t.Errorf("note = %q, want %q", fail.Note, "1 new finding")
	}
	if len(fail.Findings) == 0 {
		t.Error("failing gate must carry its findings")
	}
}

func TestGateStepFallbackWithoutMarker(t *testing.T) {
	step := gateStep("staticcheck-extra", report.GateMarker{}, false, 2, []string{"boom"})
	if step.Status != report.StatusFailed {
		t.Errorf("no-marker failure should be failed, got %+v", step)
	}
	if len(step.Findings) == 0 {
		t.Error("fallback must surface captured output so nothing is hidden")
	}
}
