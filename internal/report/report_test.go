// Package report tests the single clean-report renderer: the aligned status
// table, the per-failure detail blocks, and the verdict line.
package report

import (
	"strings"
	"testing"
)

func TestRenderCleanRun(t *testing.T) {
	got := Render(Report{
		Title: "go-mk build-check",
		Steps: []StepResult{
			{Name: "vet", Status: StatusOK},
			{Name: "lint-golangci", Status: StatusOK},
			{Name: "staticcheck-extra", Status: StatusOK},
		},
	})
	want := "go-mk build-check\n\n" +
		"  vet                ok\n" +
		"  lint-golangci      ok\n" +
		"  staticcheck-extra  ok\n" +
		"\n" +
		"  All checks passed.\n"
	if got != want {
		t.Errorf("clean render mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderFailedRunShowsFindingsOnce(t *testing.T) {
	got := Render(Report{
		Title: "go-mk build-check",
		Steps: []StepResult{
			{Name: "lint-golangci", Status: StatusOK},
			{
				Name:   "lint-gocyclo",
				Status: StatusFailed,
				Note:   "1 new finding",
				Findings: []string{
					"  internal/foo/bar.go:42:1",
					"    gocyclo: complexity 21 over 20 in bigFunc",
				},
				Remediation: "run the baseline refresh and re-run the gate.",
			},
			{Name: "govulncheck", Status: StatusOK},
		},
	})

	if !strings.Contains(got, "  lint-gocyclo   FAILED  (1 new finding)\n") {
		t.Errorf("missing failed status row:\n%s", got)
	}
	if !strings.Contains(got, "    internal/foo/bar.go:42:1\n      gocyclo: complexity 21 over 20 in bigFunc\n") {
		t.Errorf("missing indented findings:\n%s", got)
	}
	if !strings.Contains(got, "    Fix: run the baseline refresh and re-run the gate.\n") {
		t.Errorf("missing fix line:\n%s", got)
	}
	if !strings.HasSuffix(got, "  1 check failed: lint-gocyclo\n") {
		t.Errorf("missing single verdict footer:\n%s", got)
	}
	if strings.Contains(got, "Diagnostics:") {
		t.Errorf("diagnostics must be suppressed on failure:\n%s", got)
	}
	if strings.Count(got, "gocyclo: complexity 21 over 20 in bigFunc") != 1 {
		t.Errorf("finding must appear exactly once:\n%s", got)
	}
}

func TestRenderPluralVerdict(t *testing.T) {
	got := Render(Report{
		Steps: []StepResult{
			{Name: "a", Status: StatusFailed},
			{Name: "b", Status: StatusFailed},
		},
	})
	if !strings.HasSuffix(got, "  2 checks failed: a, b\n") {
		t.Errorf("plural verdict mismatch:\n%s", got)
	}
}
