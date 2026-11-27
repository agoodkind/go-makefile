package main

import (
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/report"
)

func TestBaselineGateAdvisoryFindings(t *testing.T) {
	const unused = "main.go:3:6: func futureCaller is unused (unused)"
	const errcheck = "main.go:5:2: error return value is not checked (errcheck)"
	const unreachable = "main.go:3:6: unreachable func: futureCaller"
	tests := []struct {
		name     string
		gate     string
		findings []string
		message  string
		status   report.Status
		exit     int
	}{
		{
			name:     "unused only",
			gate:     "golangci-lint",
			findings: []string{unused},
			message:  "(unused)",
			status:   report.StatusAdvisory,
			exit:     0,
		},
		{
			name:     "unchecked error",
			gate:     "golangci-lint",
			findings: []string{unused, errcheck},
			message:  "(errcheck)",
			status:   report.StatusFailed,
			exit:     1,
		},
		{
			name:     "unreachable function",
			gate:     "deadcode",
			findings: []string{unreachable},
			message:  "unreachable func",
			status:   report.StatusAdvisory,
			exit:     0,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				t.Chdir(t.TempDir())
				gateCollecting = true
				t.Cleanup(func() { gateCollecting = false })

				result, exit := runOneGate(
					test.gate,
					func() int {
						passed, err := runGateAndPrint(
							test.gate,
							test.findings,
							"baseline.txt",
							"",
							"",
							"",
						)
						if err != nil {
							t.Fatal(err)
						}

						if !passed {
							return 1
						}

						return 0
					},
				)

				if result.Status != test.status || exit != test.exit {
					t.Fatalf(
						"got status %v and exit %d, want %v and %d",
						result.Status,
						exit,
						test.status,
						test.exit,
					)
				}

				if !strings.Contains(strings.Join(result.Findings, "\n"), test.message) {
					t.Errorf("report omitted %q: %v", test.message, result.Findings)
				}
			},
		)
	}
}

func TestGateStepFromMarker(t *testing.T) {
	pass := gateStep(
		"lint-format",
		report.GateMarker{Name: "lint-format", Passed: true},
		true,
		0,
		nil,
	)
	if pass.Status != report.StatusOK {
		t.Errorf("passing gate should be ok, got %+v", pass)
	}

	fail := gateStep(
		"lint-gocyclo",
		report.GateMarker{
			Name:        "lint-gocyclo",
			Passed:      false,
			Findings:    []string{"a.go:42:1: gocyclo: too complex"},
			Remediation: "refresh the baseline.",
		},
		true,
		1,
		nil,
	)
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
