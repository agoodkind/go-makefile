package baseline

import (
	"encoding/json"
	"fmt"
	"strings"
)

// changePhrase renders the net change to one baseline as neutral counts. It
// names no mode and suggests no follow-up action.
func changePhrase(statistics Stats) string {
	var parts []string
	if statistics.Added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", statistics.Added))
	}
	if statistics.Removed > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", statistics.Removed))
	}
	if len(parts) == 0 {
		return "no change"
	}
	return strings.Join(parts, ", ")
}

// remainingPhrase renders the third column: the before/after key counts, or
// "no existing" when the baseline was empty and stayed empty. before is the old
// key set, which partitions exactly into removed and refreshed.
func remainingPhrase(statistics Stats) string {
	before := statistics.Removed + statistics.Refreshed
	if before == 0 && statistics.Remaining == 0 {
		return "no existing"
	}
	return fmt.Sprintf("%d -> %d", before, statistics.Remaining)
}

// singleLines renders one component's result for a single-component run.
func singleLines(statistics Stats) []string {
	return []string{
		statistics.Label + " baseline",
		fmt.Sprintf("  %s, %d remaining.", changePhrase(statistics), statistics.Remaining),
	}
}

// rollupLines renders a multi-component run: one aligned row per component plus
// a closing summary.
func rollupLines(all []Stats) []string {
	count := len(all)
	plural := "s"
	if count == 1 {
		plural = ""
	}
	lines := []string{
		fmt.Sprintf("Updating %d baseline%s", count, plural),
		"",
	}
	labelWidth := 0
	changeWidth := 0
	for _, statistics := range all {
		if len(statistics.Label) > labelWidth {
			labelWidth = len(statistics.Label)
		}
		if phraseLength := len(changePhrase(statistics)); phraseLength > changeWidth {
			changeWidth = phraseLength
		}
	}
	totalRemaining := 0
	for _, statistics := range all {
		totalRemaining += statistics.Remaining
		label := fmt.Sprintf("%-*s", labelWidth, statistics.Label)
		change := fmt.Sprintf("%-*s", changeWidth, changePhrase(statistics))
		lines = append(lines, fmt.Sprintf("  %s   %s   %s", label, change, remainingPhrase(statistics)))
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf(
		"  Done. %d remaining across %d baseline%s.", totalRemaining, count, plural))
	return lines
}

// RenderText writes the human-readable report to the writer. A single component
// gets the compact form; multiple components get the roll-up.
func RenderText(all []Stats) string {
	if len(all) == 0 {
		return ""
	}
	var lines []string
	if len(all) == 1 {
		lines = singleLines(all[0])
	} else {
		lines = rollupLines(all)
	}
	return strings.Join(lines, "\n") + "\n"
}

type baselineEntry struct {
	Label            string `json:"label"`
	File             string `json:"file"`
	FindingsCaptured int    `json:"findingsCaptured"`
	Added            int    `json:"added"`
	Refreshed        int    `json:"refreshed"`
	Removed          int    `json:"removed"`
	Covered          int    `json:"covered"`
	Remaining        int    `json:"remaining"`
	Changed          bool   `json:"changed"`
}

type totals struct {
	Added     int `json:"added"`
	Refreshed int `json:"refreshed"`
	Removed   int `json:"removed"`
	Remaining int `json:"remaining"`
	Baselines int `json:"baselines"`
}

type document struct {
	Scope     string          `json:"scope"`
	Baselines []baselineEntry `json:"baselines"`
	Totals    totals          `json:"totals"`
}

// RenderJSON returns the machine-readable report. The document carries neutral
// count fields and no mode name. Field names match the Swift implementation so
// the two repos share one schema.
func RenderJSON(all []Stats) (string, error) {
	entries := make([]baselineEntry, 0, len(all))
	scope := "all"
	var total totals
	total.Baselines = len(all)
	for _, statistics := range all {
		if statistics.ScopePattern != "" {
			scope = statistics.ScopePattern
		}
		entries = append(entries, baselineEntry{
			Label:            statistics.Label,
			File:             statistics.BaselinePath,
			FindingsCaptured: statistics.FindingsCaptured,
			Added:            statistics.Added,
			Refreshed:        statistics.Refreshed,
			Removed:          statistics.Removed,
			Covered:          statistics.Covered,
			Remaining:        statistics.Remaining,
			Changed:          !statistics.IsNoop(),
		})
		total.Added += statistics.Added
		total.Refreshed += statistics.Refreshed
		total.Removed += statistics.Removed
		total.Remaining += statistics.Remaining
	}
	encoded, err := json.MarshalIndent(document{
		Scope:     scope,
		Baselines: entries,
		Totals:    total,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}
