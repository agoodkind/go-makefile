package staticcheckresult_test

import (
	"regexp"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/lintgate"
	"goodkind.io/go-makefile/internal/staticcheckresult"
)

func TestIncompleteAnalysisComparesObservedDiagnostics(t *testing.T) {
	const formatterError = "multiline_call_arguments: format call arguments in formatter.go: source layout did not converge after 20 rounds"
	const savedFinding = "cmd/app/main.go:8:1: commandentry: delegate implementation to an importable package [id=0123456789abcdef0123456789abcdef:1]"
	const excludedFinding = "generated/main.go:8:1: commandentry: delegate implementation to an importable package [id=11111111111111111111111111111111:1]"
	const unseenFinding = "unseen/main.go:8:1: commandentry: delegate implementation to an importable package [id=22222222222222222222222222222222:1]"
	const newFinding = "cmd/new/main.go:8:1: commandentry: delegate implementation to an importable package [id=33333333333333333333333333333333:1]"

	for _, testCase := range []struct {
		name     string
		findings []string
		newCount int
	}{
		{name: "all observed findings match", findings: []string{savedFinding, excludedFinding}},
		{
			name:     "new observed finding remains",
			findings: []string{savedFinding, excludedFinding, newFinding},
			newCount: 1,
		},
	} {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				lines := append(
					[]string{formatterError, "Additional formatter failure details."},
					testCase.findings...,
				)
				result := staticcheckresult.Parse(1, lines)
				if result.Complete() || len(
					result.PackageErrors,
				) != 0 || len(result.ToolErrors) != 2 ||
					result.ToolErrors[0] != formatterError {
					t.Fatalf(
						"analysis failures did not remain separate from diagnostics: %+v",
						result,
					)
				}

				gate := staticcheckresult.Evaluate(
					result,
					[]string{savedFinding, unseenFinding},
					regexp.MustCompile(`^generated/`),
					regexp.MustCompile(`commandentry:`),
					"Fix the analysis errors.",
				)
				if gate.Passed || gate.GoneCount != 0 || len(
					gate.NewFindings,
				) != testCase.newCount {
					t.Fatalf("incomplete analysis produced an incorrect gate verdict: %+v", gate)
				}

				for _, finding := range gate.NewFindings {
					if finding != newFinding {
						t.Fatalf("the gate did not apply its baseline or exclusion: %s", finding)
					}
				}

				if output := strings.Join(lintgate.Render(gate), "\n"); strings.Contains(
					output,
					"now fixed",
				) {
					t.Fatalf("incomplete analysis marked an unseen finding fixed:\n%s", output)
				}

				if failures := strings.Join(result.FailureLines(), "\n"); !strings.Contains(
					failures,
					"Analyzer or tool errors:",
				) ||
					strings.Contains(failures, savedFinding) {
					t.Fatalf("failure output mislabeled ordinary diagnostics:\n%s", failures)
				}
			},
		)
	}
}
