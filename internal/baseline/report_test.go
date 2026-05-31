package baseline

import (
	"encoding/json"
	"strings"
	"testing"
)

// forbiddenTokens are words the output contract bans from any user-facing
// baseline output: anything that names an update mode or hints at pruning,
// accepting, skipping, or disabling a baseline.
var forbiddenTokens = []string{
	"prune", "accept-new", "accept new", "remove-fixed", "removefixed",
	"skip", "disable", "silence", "weaken", "circumvent",
	"mode:", "prunefixed", "acceptnew",
}

func assertNeutral(t *testing.T, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, token := range forbiddenTokens {
		if strings.Contains(lower, token) {
			t.Errorf("forbidden token %q in output:\n%s", token, text)
		}
	}
}

func makeStats(label string, added, removed, refreshed, remaining int) Stats {
	return Stats{
		Label:        label,
		BaselinePath: "." + label + "-baseline.txt",
		Added:        added,
		Removed:      removed,
		Refreshed:    refreshed,
		Remaining:    remaining,
	}
}

func TestChangePhraseIsNeutral(t *testing.T) {
	cases := []struct {
		stats Stats
		want  string
	}{
		{makeStats("a", 0, 2, 0, 0), "2 removed"},
		{makeStats("a", 3, 0, 0, 0), "3 added"},
		{makeStats("a", 3, 1, 0, 0), "3 added, 1 removed"},
		{makeStats("a", 0, 0, 9, 0), "no change"},
		{makeStats("a", 0, 0, 0, 0), "no change"},
	}
	for _, testCase := range cases {
		if got := changePhrase(testCase.stats); got != testCase.want {
			t.Errorf("changePhrase = %q, want %q", got, testCase.want)
		}
	}
}

func TestRenderTextSingleIsNeutral(t *testing.T) {
	text := RenderText([]Stats{makeStats("golangci-lint", 0, 2, 0, 17)})
	if !strings.Contains(text, "golangci-lint baseline") {
		t.Errorf("missing header: %s", text)
	}
	if !strings.Contains(text, "2 removed, 17 remaining.") {
		t.Errorf("missing neutral counts: %s", text)
	}
	assertNeutral(t, text)
}

func TestRenderTextRollupListsEachAndSummary(t *testing.T) {
	text := RenderText([]Stats{
		makeStats("golangci-lint", 0, 2, 0, 17),
		makeStats("gocyclo", 0, 0, 4, 0),
		makeStats("deadcode", 0, 0, 0, 0),
		makeStats("staticcheck-extra", 0, 1, 0, 14),
	})
	if !strings.HasPrefix(text, "Updating 4 baselines") {
		t.Errorf("missing rollup header: %s", text)
	}
	if !strings.Contains(text, "no change") {
		t.Errorf("missing no-change line: %s", text)
	}
	if !strings.Contains(text, "no existing") {
		t.Errorf("missing no-existing column: %s", text)
	}
	if !strings.Contains(text, "4 -> 0") {
		t.Errorf("missing before/after column: %s", text)
	}
	if !strings.Contains(text, "Done. 31 remaining across 4 baselines.") {
		t.Errorf("missing summary: %s", text)
	}
	assertNeutral(t, text)
}

func TestRenderJSONHasNeutralFieldsAndNoMode(t *testing.T) {
	stats := []Stats{
		{
			Label: "golangci-lint", BaselinePath: ".golangci-lint-baseline.txt",
			FindingsCaptured: 69, Added: 0, Refreshed: 17, Removed: 2, Covered: 69, Remaining: 17,
		},
		{Label: "gocyclo", BaselinePath: ".gocyclo-baseline.txt"},
	}
	text, err := RenderJSON(stats)
	if err != nil {
		t.Fatal(err)
	}
	assertNeutral(t, text)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatal(err)
	}
	if _, hasMode := parsed["mode"]; hasMode {
		t.Error("document must not carry a mode field")
	}
	baselines, ok := parsed["baselines"].([]any)
	if !ok || len(baselines) != 2 {
		t.Fatalf("expected 2 baselines, got %v", parsed["baselines"])
	}
	first, ok := baselines[0].(map[string]any)
	if !ok {
		t.Fatal("baseline entry is not an object")
	}
	if _, hasMode := first["mode"]; hasMode {
		t.Error("baseline entry must not carry a mode field")
	}
	if first["removed"].(float64) != 2 {
		t.Errorf("removed = %v, want 2", first["removed"])
	}
	if first["changed"].(bool) != true {
		t.Error("changed should be true for a changed baseline")
	}
	second := baselines[1].(map[string]any)
	if second["changed"].(bool) != false {
		t.Error("changed should be false for a no-op baseline")
	}
}
