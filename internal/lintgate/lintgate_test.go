// Package lintgate tests the baseline-diff gate port: the set logic of
// Evaluate across clean, new, fixed, line-shifted, excluded, and scoped cases,
// and the neutral-output contract of Render for both a FAILED and an OK result.
//
// =============================================================================
// lintgate tests
// =============================================================================
package lintgate_test

import (
	"regexp"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/lintgate"
)

// assertNeutral fails the test when text contains any token that names a
// baseline update mode or a softening verb, copied from
// internal/baseline/report_test.go so the gate text stays neutral.
func assertNeutral(t *testing.T, text string) {
	t.Helper()
	forbidden := []string{
		"prune",
		"accept",
		"remove-fixed",
		"skip",
		"disable",
		"silence",
		"weaken",
	}
	lowered := strings.ToLower(text)
	for _, token := range forbidden {
		if strings.Contains(lowered, token) {
			t.Errorf("neutral output contains forbidden token %q in:\n%s", token, text)
		}
	}
}

func TestEvaluate(t *testing.T) {
	testCases := []struct {
		name            string
		current         []string
		baseline        []string
		excludeRegexp   *regexp.Regexp
		scopeRegexp     *regexp.Regexp
		wantPassed      bool
		wantNewFindings []string
		wantGoneCount   int
	}{
		{
			name:            "clean pass when current keys are a subset of baseline",
			current:         []string{"a.go:1:1: issue one", "b.go:2:2: issue two"},
			baseline:        []string{"a.go:1:1: issue one", "b.go:2:2: issue two", "c.go:3:3: issue three"},
			wantPassed:      true,
			wantNewFindings: []string{},
			wantGoneCount:   1,
		},
		{
			name:            "new finding fails the gate",
			current:         []string{"a.go:1:1: issue one", "new.go:5:5: brand new issue"},
			baseline:        []string{"a.go:1:1: issue one"},
			wantPassed:      false,
			wantNewFindings: []string{"new.go:5:5: brand new issue"},
			wantGoneCount:   0,
		},
		{
			name:            "fixed finding raises gone count",
			current:         []string{"a.go:1:1: issue one"},
			baseline:        []string{"a.go:1:1: issue one", "gone.go:9:9: fixed issue"},
			wantPassed:      true,
			wantNewFindings: []string{},
			wantGoneCount:   1,
		},
		{
			name:            "line-shifted finding keeps its key and still passes",
			current:         []string{"a.go:42:7: issue one"},
			baseline:        []string{"a.go:1:1: issue one"},
			wantPassed:      true,
			wantNewFindings: []string{},
			wantGoneCount:   0,
		},
		{
			name:            "exclude filter drops a would-be new finding",
			current:         []string{"a.go:1:1: issue one", "vendor/x.go:2:2: vendored issue"},
			baseline:        []string{"a.go:1:1: issue one"},
			excludeRegexp:   regexp.MustCompile("vendor/"),
			wantPassed:      true,
			wantNewFindings: []string{},
			wantGoneCount:   0,
		},
		{
			name:            "scope filter limits comparison to matching lines",
			current:         []string{"a.go:1:1: scoped issue", "b.go:2:2: out of scope new"},
			baseline:        []string{"a.go:1:1: scoped issue"},
			scopeRegexp:     regexp.MustCompile(`^a\.go:`),
			wantPassed:      true,
			wantNewFindings: []string{},
			wantGoneCount:   0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := lintgate.Evaluate(
				"smoke-gate",
				testCase.current,
				testCase.baseline,
				testCase.excludeRegexp,
				testCase.scopeRegexp,
				"Run the baseline refresh and re-run the gate.",
			)
			if result.Passed != testCase.wantPassed {
				t.Errorf("Passed = %v, want %v", result.Passed, testCase.wantPassed)
			}
			if !equalLines(result.NewFindings, testCase.wantNewFindings) {
				t.Errorf("NewFindings = %#v, want %#v", result.NewFindings, testCase.wantNewFindings)
			}
			if result.GoneCount != testCase.wantGoneCount {
				t.Errorf("GoneCount = %d, want %d", result.GoneCount, testCase.wantGoneCount)
			}
		})
	}
}

func TestRenderNeutral(t *testing.T) {
	failed := lintgate.Evaluate(
		"smoke-gate",
		[]string{"new.go:5:5: brand new issue"},
		nil,
		nil,
		nil,
		"Run the baseline refresh and re-run the gate.",
	)
	failedText := strings.Join(lintgate.Render(failed), "\n")
	if !strings.Contains(failedText, "smoke-gate: FAILED") {
		t.Errorf("FAILED render missing header in:\n%s", failedText)
	}
	if !strings.Contains(failedText, "New findings: 1") {
		t.Errorf("FAILED render missing new-findings count in:\n%s", failedText)
	}
	assertNeutral(t, failedText)

	ok := lintgate.Evaluate(
		"smoke-gate",
		[]string{"a.go:1:1: issue one"},
		[]string{"a.go:1:1: issue one", "gone.go:9:9: fixed issue"},
		nil,
		nil,
		"Run the baseline refresh and re-run the gate.",
	)
	okText := strings.Join(lintgate.Render(ok), "\n")
	if !strings.Contains(okText, "smoke-gate: OK") {
		t.Errorf("OK render missing header in:\n%s", okText)
	}
	if !strings.Contains(okText, "Saved findings now fixed: 1") {
		t.Errorf("OK render missing saved-findings line in:\n%s", okText)
	}
	assertNeutral(t, okText)
}

// equalLines reports whether two string slices hold the same elements in the
// same order, treating nil and empty as equal.
func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
