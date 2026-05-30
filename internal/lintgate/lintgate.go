// Package lintgate ports the baseline-diff gate go_mk_run_baseline_diff_gate
// from scripts/go-mk-common.sh into pure Go. The gate compares the current
// findings against a saved baseline, keyizing both sides through
// internal/findings so a line that only shifted position keeps the same
// identity, and reports any current finding whose key is absent from the
// baseline as a new finding that fails the gate. The package never touches the
// filesystem, the clock, or the process streams: it returns a GateResult and a
// rendered block of lines, and the command layer in cmd/go-mk owns reading
// findings, applying the regexps, and printing. The rendered text reproduces
// the shell printf strings byte-for-byte and obeys the neutral-output contract,
// so it never names a baseline update mode or the words prune, accept,
// remove-fixed, skip, disable, silence, or weaken.
//
// =============================================================================
// lintgate
// =============================================================================
package lintgate

import (
	"regexp"
	"sort"
	"strconv"

	"goodkind.io/go-makefile/internal/findings"
)

// GateResult is the pure outcome of evaluating one baseline-diff gate. Passed
// is false exactly when NewFindings is non-empty, mirroring the shell guard
// that fails the gate when the mapped new-findings file is non-empty.
// NewFindings holds the current finding lines whose key is absent from the
// baseline, in their original input order. GoneCount is the number of baseline
// keys absent from the current findings, matching comm -13 over the two
// key sets. Remediation is the caller-supplied remediation string the renderer
// emits on a failure.
type GateResult struct {
	Gate        string
	Passed      bool
	NewFindings []string
	GoneCount   int
	Remediation string
	// SuppressFixedCount drops the "Saved findings now fixed" line on a pass
	// even when GoneCount is positive, mirroring the shell suppress_fixed_count
	// flag the staticcheck-extra gate sets when flags are enabled without a
	// scope so a fixed count it cannot attribute to a scope is not reported.
	SuppressFixedCount bool
}

// Evaluate reproduces the set logic of go_mk_run_baseline_diff_gate as a pure
// function. It first applies the exclude filter (dropping every line the
// exclude regexp matches, mirroring grep -Ev) and then the scope filter
// (keeping only lines the scope regexp matches, mirroring grep -E) to both the
// current findings and the baseline findings, treating a nil regexp as the
// shell's empty-pattern pass-through. It keyizes both filtered sides through
// findings.Key and computes the new keys (present in current, absent from
// baseline) and the gone count (present in baseline, absent from current) with
// comm -23 and comm -13 semantics over sorted, unique key sets. The new keys
// are mapped back to the current finding lines that produced them, preserving
// input order, so a line that only shifted position keeps its baseline key and
// is not reported. Evaluate performs no printing and no file I/O.
//
// ---- Evaluate ----
func Evaluate(
	gateName string,
	currentFindings, baselineFindings []string,
	excludeRegexp, scopeRegexp *regexp.Regexp,
	remediation string,
) GateResult {
	current := applyFilters(currentFindings, excludeRegexp, scopeRegexp)
	baseline := applyFilters(baselineFindings, excludeRegexp, scopeRegexp)

	currentKeys := keySet(current)
	baselineKeys := keySet(baseline)

	newFindings := mapNewFindings(current, baselineKeys)
	goneCount := countGoneKeys(currentKeys, baselineKeys)

	return GateResult{
		Gate:        gateName,
		Passed:      len(newFindings) == 0,
		NewFindings: newFindings,
		GoneCount:   goneCount,
		Remediation: remediation,
	}
}

// applyFilters drops lines matching the exclude regexp and then keeps only
// lines matching the scope regexp, reproducing go_mk_filter_file followed by
// go_mk_scope_file. A nil regexp is the shell empty-pattern case and leaves the
// lines untouched. The returned slice preserves input order and is never nil.
func applyFilters(lines []string, excludeRegexp, scopeRegexp *regexp.Regexp) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if excludeRegexp != nil && excludeRegexp.MatchString(line) {
			continue
		}
		if scopeRegexp != nil && !scopeRegexp.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// keySet keyizes each line through findings.Key with empty pwd and cwd, since
// the gate runs over findings the capture layer already normalized, and returns
// the unique keys. This mirrors go_mk_keyize_file, which pipes the lines through
// awk action=key and then sort -u.
func keySet(lines []string) map[string]struct{} {
	keys := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		keys[findings.Key(line, "", "")] = struct{}{}
	}
	return keys
}

// mapNewFindings returns the current finding lines whose key is absent from the
// baseline key set, preserving input order and de-duplicating by key so a line
// that shares a key with an earlier line is reported once. This reproduces the
// comm -23 new-keys set mapped back to the current findings via
// go_mk_map_keys_to_findings, where the mapped output is sorted-unique by key.
func mapNewFindings(current []string, baselineKeys map[string]struct{}) []string {
	newFindings := make([]string, 0, len(current))
	seen := make(map[string]struct{}, len(current))
	for _, line := range current {
		key := findings.Key(line, "", "")
		if _, inBaseline := baselineKeys[key]; inBaseline {
			continue
		}
		if _, already := seen[key]; already {
			continue
		}
		seen[key] = struct{}{}
		newFindings = append(newFindings, line)
	}
	return newFindings
}

// countGoneKeys counts the baseline keys absent from the current key set,
// reproducing the comm -13 gone-keys set over the two sorted-unique key sets.
// It sorts the gone keys only for determinism; the count is independent of
// order.
func countGoneKeys(currentKeys, baselineKeys map[string]struct{}) int {
	gone := make([]string, 0, len(baselineKeys))
	for key := range baselineKeys {
		if _, inCurrent := currentKeys[key]; inCurrent {
			continue
		}
		gone = append(gone, key)
	}
	sort.Strings(gone)
	return len(gone)
}

// Render returns the gate's user-facing lines, reproducing the printf strings
// of go_mk_run_baseline_diff_gate byte-for-byte and obeying the neutral-output
// contract. On a failure it emits "<gate>: FAILED", "  New findings: N", a
// blank line, "Findings:", each new finding through findings.Print, a blank
// line, and the indented remediation. On a pass it emits "<gate>: OK",
// "  New findings: 0", and, when GoneCount is positive, "  Saved findings now
// fixed: N". Each element of the returned slice is one output line without a
// trailing newline; the command layer joins them with newlines when printing.
//
// ---- Render ----
func Render(result GateResult) []string {
	if !result.Passed {
		return renderFailed(result)
	}
	return renderPassed(result)
}

// renderFailed builds the FAILED block. The findings.Print output carries its
// own newlines for the two-line location/message form, so each printed finding
// is split into the lines it contains to keep one slice element per output line.
func renderFailed(result GateResult) []string {
	lines := make([]string, 0, len(result.NewFindings)*2+5)
	lines = append(lines, result.Gate+": FAILED")
	lines = append(lines, "  New findings: "+strconv.Itoa(len(result.NewFindings)))
	lines = append(lines, "")
	lines = append(lines, "Findings:")
	for _, finding := range result.NewFindings {
		lines = append(lines, printedLines(finding)...)
	}
	lines = append(lines, "")
	lines = append(lines, "  "+result.Remediation)
	return lines
}

// renderPassed builds the OK block, appending the saved-findings-now-fixed line
// only when GoneCount is positive, matching the shell guard gone_count > 0.
func renderPassed(result GateResult) []string {
	lines := make([]string, 0, 3)
	lines = append(lines, result.Gate+": OK")
	lines = append(lines, "  New findings: 0")
	if result.GoneCount > 0 && !result.SuppressFixedCount {
		lines = append(lines, "  Saved findings now fixed: "+strconv.Itoa(result.GoneCount))
	}
	return lines
}

// printedLines renders one finding through findings.Print and splits the result
// on newlines into individual output lines, dropping the trailing empty element
// the final newline produces.
func printedLines(finding string) []string {
	rendered := findings.Print(finding, "", "")
	split := splitLines(rendered)
	return split
}

// splitLines splits text on '\n' and drops a single trailing empty element so a
// string ending in a newline does not yield a spurious blank line.
func splitLines(text string) []string {
	out := make([]string, 0, 2)
	start := 0
	for index := 0; index < len(text); index++ {
		if text[index] == '\n' {
			out = append(out, text[start:index])
			start = index + 1
		}
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}
