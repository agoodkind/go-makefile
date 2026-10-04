package mutation_test

import (
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/mutation"
)

// The reports are gremlins v0.6.0 output for one package with three mutants.
const (
	killedReport = `{"go_module":"example.com/app","files":[{"file_name":"gate.go","mutations":[{"type":"CONDITIONALS_NEGATION","status":"KILLED","line":46,"column":14},{"type":"CONDITIONALS_NEGATION","status":"KILLED","line":46,"column":30},{"type":"CONDITIONALS_NEGATION","status":"LIVED","line":49,"column":18}]}],"test_efficacy":66.66,"mutations_coverage":100,"mutants_total":3,"mutants_killed":2,"mutants_lived":1,"mutants_not_viable":0,"mutants_not_covered":0,"elapsed_time":1.69}`

	timedOutReport = `{"go_module":"example.com/app","files":[{"file_name":"gate.go","mutations":[{"type":"CONDITIONALS_NEGATION","status":"TIMED OUT","line":49,"column":18},{"type":"CONDITIONALS_NEGATION","status":"TIMED OUT","line":46,"column":14},{"type":"CONDITIONALS_NEGATION","status":"TIMED OUT","line":46,"column":30}]}],"test_efficacy":0,"mutations_coverage":0,"mutants_total":0,"mutants_killed":0,"mutants_lived":0,"mutants_not_viable":0,"mutants_not_covered":0,"elapsed_time":0.89}`
)

func summarize(t *testing.T, reports map[string]string, order []string) mutation.Summary {
	t.Helper()

	packageReports := make([]mutation.PackageReport, 0, len(order))
	for _, name := range order {
		report, err := mutation.ParseReport([]byte(reports[name]))
		if err != nil {
			t.Fatalf("ParseReport(%s): %v", name, err)
		}
		packageReports = append(packageReports, mutation.PackageReport{Package: name, Report: report})
	}
	return mutation.Summarize(packageReports)
}

func TestSummaryScoresKilledOverCovered(t *testing.T) {
	summary := summarize(t, map[string]string{"gate": killedReport}, []string{"gate"})

	score, defined := summary.Score()
	if !defined {
		t.Fatal("Score is undefined for a run with covered mutants")
	}
	if score < 66.6 || score > 66.7 {
		t.Fatalf("Score = %.2f, want 2 killed of 3 covered", score)
	}
	if !summary.MeetsMinimum(0) {
		t.Fatal("MeetsMinimum(0) = false, want true with no minimum")
	}
	if !summary.MeetsMinimum(66) {
		t.Fatal("MeetsMinimum(66) = false, want true for a score of 66.67")
	}
	if summary.MeetsMinimum(67) {
		t.Fatal("MeetsMinimum(67) = true, want false for a score of 66.67")
	}
}

func TestSummaryTotalsPackagesAndCountsTimedOutMutants(t *testing.T) {
	summary := summarize(
		t,
		map[string]string{"gate": killedReport, "lint": timedOutReport},
		[]string{"gate", "lint"},
	)

	if summary.Killed != 2 || summary.Lived != 1 || summary.TimedOut != 3 {
		t.Fatalf("totals = killed %d, lived %d, timed out %d; want 2, 1, 3",
			summary.Killed, summary.Lived, summary.TimedOut)
	}

	rendered := mutation.RenderMarkdown(summary, 80)
	wantLines := []string{
		"| gate | 2 | 1 | 0 | 0 | 0 | 66.67% |",
		"| lint | 0 | 0 | 0 | 3 | 0 | n/a |",
		"| Total | 2 | 1 | 0 | 3 | 0 | 66.67% |",
		"3 timed-out mutants do not count toward the score.",
		"The run is below the minimum score of 80.00%.",
	}
	for _, want := range wantLines {
		if !strings.Contains(rendered, want) {
			t.Fatalf("summary lacks %q:\n%s", want, rendered)
		}
	}
}

func TestRunWithOnlyTimedOutMutantsFailsAPositiveMinimum(t *testing.T) {
	summary := summarize(t, map[string]string{"lint": timedOutReport}, []string{"lint"})

	if _, defined := summary.Score(); defined {
		t.Fatal("Score is defined for a run with no covered mutant")
	}
	if summary.MeetsMinimum(1) {
		t.Fatal("MeetsMinimum(1) = true, want false when no mutant was covered")
	}
	if !summary.MeetsMinimum(0) {
		t.Fatal("MeetsMinimum(0) = false, want true with no minimum")
	}
}
