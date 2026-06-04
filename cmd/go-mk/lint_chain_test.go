package main

import (
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/report"
)

func TestExtractMarkerStripsAndParses(t *testing.T) {
	line, err := report.EncodeMarker(report.GateMarker{
		Name:     "lint-gocyclo",
		Passed:   false,
		Findings: []string{"a.go:1:1: too complex"},
	})
	if err != nil {
		t.Fatalf("EncodeMarker: %v", err)
	}
	captured := []string{"go: downloading something", line, "tool noise"}
	marker, rest, found := extractMarker(captured)
	if !found {
		t.Fatal("expected to find a marker")
	}
	if marker.Name != "lint-gocyclo" || marker.Passed {
		t.Errorf("unexpected marker: %+v", marker)
	}
	for _, leftover := range rest {
		if strings.HasPrefix(leftover, report.MarkerPrefix) {
			t.Errorf("marker line was not stripped: %q", leftover)
		}
	}
	if len(rest) != 2 {
		t.Errorf("rest should keep the non-marker lines, got %v", rest)
	}
}

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
