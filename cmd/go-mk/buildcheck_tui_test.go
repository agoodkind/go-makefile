package main

import (
	"testing"

	"goodkind.io/go-makefile/internal/report"
)

func TestFinishedCheckViewKeepsStatusTableOnly(t *testing.T) {
	checks := []check{{name: "check-1"}, {name: "check-2"}}
	model := newCheckModel("go-mk check", len("check-2"), checks)
	model.steps[0] = stepView{
		name: "check-1",
		done: true,
		result: report.StepResult{
			Name:   "check-1",
			Status: report.StatusOK,
		},
	}
	model.steps[1] = stepView{
		name: "check-2",
		done: true,
		result: report.StepResult{
			Name:     "check-2",
			Status:   report.StatusFailed,
			Findings: []string{"failure detail"},
		},
	}
	want := "go-mk check\n\n  check-1  ok\n  check-2  FAILED"
	if got := model.View().Content; got != want {
		t.Fatalf("finished view = %q, want %q", got, want)
	}
}

func TestCheckCompletionDetailsExcludeStatusRows(t *testing.T) {
	results := []report.StepResult{
		{Name: "check-1", Status: report.StatusOK},
		{
			Name:     "check-2",
			Status:   report.StatusFailed,
			Findings: []string{"failure detail"},
		},
	}

	want := "\n  check-2\n  failure detail\n\n  1 check failed: check-2\n"
	if got := checkCompletionDetails(results); got != want {
		t.Fatalf("completion details = %q, want %q", got, want)
	}
}
