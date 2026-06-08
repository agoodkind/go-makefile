// Package findings ports the finding-transform logic from
// scripts/go-mk-findings.awk into pure Go. Each transform takes finding lines
// and the same parameters the awk reads from its -v assignments and file
// arguments, then returns the transformed lines. The package never touches the
// filesystem, the clock, or the process streams: the command layer in
// cmd/go-mk owns stdin, stdout, and file reads, mirroring the internal/baseline
// split. Output is matched byte-for-byte against the awk by the oracle test, so
// the ":" joins, the "\t" columns in baseline and range rows, and the
// per-action newline handling deliberately reproduce the awk.
//
// =============================================================================
// findings
// =============================================================================
package findings

import (
	"regexp"
	"strconv"
	"strings"
)

// locationPattern matches the :line:col: run the awk collapses for keys and
// splits on for printing. It mirrors the awk regexp /:[0-9]+:[0-9]+:/ exactly.
var locationPattern = regexp.MustCompile(`:[0-9]+:[0-9]+:`)

// blankPattern matches a line that is empty or only spaces and tabs, mirroring
// the awk /^[ \t]*$/ test that drops blank baseline lines.
var blankPattern = regexp.MustCompile(`^[ \t]*$`)

// NormalizePath strips a leading pwd prefix, then a leading cwd prefix, then any
// number of leading "../" segments, in that order, matching the awk
// normalize_path. The prefixes are removed only when they sit at the very start
// of the line, and each removal feeds the next, so a pwd that is a prefix of cwd
// is handled by the sequential checks just as the awk does with index()==1.
//
// ---- NormalizePath ----
func NormalizePath(line, pwd, cwd string) string {
	out := line
	if pwd != "" && strings.HasPrefix(out, pwd) {
		out = out[len(pwd):]
	}
	if cwd != "" && strings.HasPrefix(out, cwd) {
		out = out[len(cwd):]
	}
	for strings.HasPrefix(out, "../") {
		out = out[len("../"):]
	}
	return out
}

// Key reduces a finding line to a stable identity by normalizing the path and
// then replacing the first :line:col: run with :::, matching the awk key_for.
// Lines without a :line:col: run return their normalized form unchanged, so a
// line with no colon is just the normalized path.
//
// ---- Key ----
func Key(line, pwd, cwd string) string {
	out := NormalizePath(line, pwd, cwd)
	location := locationPattern.FindStringIndex(out)
	if location == nil {
		return out
	}
	return out[:location[0]] + ":::" + out[location[1]:]
}

// FindingPath returns the file path in front of the first :line:col: run in
// line, and false when the line has no such run (so it has no parseable path).
// It reuses locationPattern, the same split Key and Print use, so the path is
// the substring before the first ":line:col:".
//
// ---- FindingPath ----
func FindingPath(line string) (string, bool) {
	location := locationPattern.FindStringIndex(line)
	if location == nil {
		return "", false
	}
	return line[:location[0]], true
}

// Baseline returns the baseline payload for a finding line, matching the awk
// baseline_finding. It drops blank or whitespace-only lines and lines that begin
// with a hash by returning the empty string and false. Otherwise it cuts the
// line at the "\t# <label>:" marker when present and returns the normalized
// remainder with true. The boolean lets the caller skip dropped lines without
// emitting a blank row, matching the awk guard if (finding != "").
//
// ---- Baseline ----
func Baseline(line, label, pwd, cwd string) (string, bool) {
	if blankPattern.MatchString(line) || strings.HasPrefix(line, "#") {
		return "", false
	}
	marker := "\t# " + label + ":"
	finding := line
	if index := strings.Index(line, marker); index >= 0 {
		finding = line[:index]
	}
	return NormalizePath(finding, pwd, cwd), true
}

// Map keeps the finding lines whose key is present in savedKeys, matching the
// awk map action where the first file populates a key set and stdin lines are
// kept when key_for is in that set. The savedKeys set holds the raw lines from
// the saved-key file exactly as the awk stores them, so callers pass the file
// lines verbatim. The returned slice preserves input order and is never nil.
//
// ---- Map ----
func Map(lines []string, savedKeys map[string]struct{}, pwd, cwd string) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if _, ok := savedKeys[Key(line, pwd, cwd)]; ok {
			kept = append(kept, line)
		}
	}
	return kept
}

// Print renders a finding line for display, matching the awk print_finding. When
// the normalized line contains a :line:col: run it returns the location
// path:line:col and the message on a second indented line with leading
// whitespace stripped, joined as the awk printf "  %s\n    %s\n" produces.
// Otherwise it returns the single indented form "  %s\n". The return value
// includes the trailing newline so the caller writes it verbatim.
//
// ---- Print ----
func Print(line, pwd, cwd string) string {
	out := NormalizePath(line, pwd, cwd)
	location := locationPattern.FindStringIndex(out)
	if location == nil {
		return "  " + out + "\n"
	}
	locationText := out[:location[1]-1]
	message := strings.TrimLeft(out[location[1]:], " \t")
	return "  " + locationText + "\n    " + message + "\n"
}

// Range is one diff hunk span the awk ranges action emits as file, start, end
// joined by tabs, and the linefilter action reads back to decide membership.
type Range struct {
	File  string
	Start int
	End   int
}

// hunkHeaderPattern matches the @@ -old +new @@ hunk header and captures the
// new-side start and optional count, mirroring how the awk reads $3 after the
// @@ field and splits on a comma.
var hunkHeaderPattern = regexp.MustCompile(`^@@ \S+ \+([0-9]+)(?:,([0-9]+))? @@`)

// Ranges parses unified-diff lines into hunk spans, matching the awk ranges
// action. It tracks the current file from "+++ " headers, dropping a leading
// "b/" and treating /dev/null as no file, and for each @@ hunk with a positive
// line count it records file, start, end where end is start plus count minus
// one. A hunk with no explicit count defaults to one line, as the awk does with
// range_parts[2] == "" ? 1. The returned slice is never nil.
//
// ---- Ranges ----
func Ranges(lines []string) []Range {
	ranges := make([]Range, 0, len(lines))
	currentFile := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "+++ ") {
			currentFile = parseDiffFile(line)
			continue
		}
		if currentFile == "" {
			continue
		}
		match := hunkHeaderPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		start, startErr := strconv.Atoi(match[1])
		if startErr != nil {
			continue
		}
		count := 1
		if match[2] != "" {
			parsed, countErr := strconv.Atoi(match[2])
			if countErr != nil {
				continue
			}
			count = parsed
		}
		if count <= 0 {
			continue
		}
		ranges = append(ranges, Range{File: currentFile, Start: start, End: start + count - 1})
	}
	return ranges
}

// parseDiffFile extracts the file path from a "+++ " diff header, dropping a
// leading "b/" and mapping /dev/null to the empty string, matching the awk's
// handling of $2 on a "+++ " line.
func parseDiffFile(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	file := strings.TrimPrefix(fields[1], "b/")
	if file == "/dev/null" {
		return ""
	}
	return file
}

// LineFilter keeps the finding lines whose file and line fall inside one of the
// ranges, matching the awk linefilter action. The file is the text before the
// first colon and the line is the integer after it, both read with awk's
// split-on-colon and numeric coercion, so a missing or non-numeric line yields
// zero and is dropped. The returned slice preserves input order and is never
// nil.
//
// ---- LineFilter ----
func LineFilter(lines []string, ranges []Range) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		file, lineNumber := findingFileAndLine(line)
		if file == "" || lineNumber == 0 {
			continue
		}
		if rangesContain(ranges, file, lineNumber) {
			kept = append(kept, line)
		}
	}
	return kept
}

// findingFileAndLine splits a finding into its file path and line number using
// the awk convention: split on colon, field one is the path, field two coerced
// to an integer is the line. awk's numeric coercion of a non-numeric or absent
// field is zero, which the numeric-prefix helper reproduces.
func findingFileAndLine(line string) (string, int) {
	parts := strings.Split(line, ":")
	file := parts[0]
	if len(parts) < 2 {
		return file, 0
	}
	lineNumber := awkNumericPrefix(parts[1])
	return file, lineNumber
}

// awkNumericPrefix reproduces awk's string-to-number coercion: it reads an
// optional leading sign and the leading run of digits and returns their value,
// yielding zero when the field has no leading digits. This matches parts[2] + 0
// in the awk for fields like "12abc" or the empty string.
func awkNumericPrefix(field string) int {
	trimmed := strings.TrimLeft(field, " \t")
	end := 0
	if end < len(trimmed) && (trimmed[end] == '+' || trimmed[end] == '-') {
		end++
	}
	digitsStart := end
	for end < len(trimmed) && trimmed[end] >= '0' && trimmed[end] <= '9' {
		end++
	}
	if end == digitsStart {
		return 0
	}
	value, err := strconv.Atoi(trimmed[:end])
	if err != nil {
		return 0
	}
	return value
}

// rangesContain reports whether the file and line fall inside any range,
// matching the awk inclusive bounds check start <= line <= end.
func rangesContain(ranges []Range, file string, lineNumber int) bool {
	for _, candidate := range ranges {
		if candidate.File != file {
			continue
		}
		if lineNumber >= candidate.Start && lineNumber <= candidate.End {
			return true
		}
	}
	return false
}
