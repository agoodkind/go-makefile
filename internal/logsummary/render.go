package logsummary

import (
	"fmt"
	"sort"
	"strings"
)

// phrasing holds the singular and plural sentence for one summary line, each
// carrying a single %d for the count. The wording follows Apple Human Interface
// Guidelines style: sentence case, plain words, and a count written as an
// English quantity rather than a symbol.
type phrasing struct {
	singular string
	plural   string
}

// bucketPhrasing maps a canonical bucket id to its wording. Several distinct
// boundary messages collapse into one bucket so the summary reads as one line
// per kind of work rather than one line per message.
var bucketPhrasing = map[string]phrasing{
	"ensure_dir":    {"Prepared the build directory %d time", "Prepared the build directory %d times"},
	"read_file":     {"Read %d file", "Read %d files"},
	"read_baseline": {"Read %d baseline", "Read %d baselines"},
	"write_file":    {"Wrote %d file", "Wrote %d files"},
	"install_tool":  {"Installed %d Go tool", "Installed %d Go tools"},
	"run_gate":      {"Ran %d gate", "Ran %d gates"},
	"run_command":   {"Ran %d command", "Ran %d commands"},
	"resolve_env":   {"Resolved %d Go environment value", "Resolved %d Go environment values"},
	"extract":       {"Extracted findings %d time", "Extracted findings %d times"},
	"notice":        {"Checked notices %d time", "Checked notices %d times"},
}

// messageBucket maps each known slog boundary message to a bucket id. Messages
// absent here fall back to a sentence built from the message itself.
var messageBucket = map[string]string{
	"lint ensure make dir":          "ensure_dir",
	"lint read file":                "read_file",
	"lint read file content":        "read_file",
	"lint read baseline":            "read_baseline",
	"lint write findings file":      "write_file",
	"lint write ranges file":        "write_file",
	"rewrite baseline file":         "write_file",
	"lint install go tool":          "install_tool",
	"lint run gate":                 "run_gate",
	"lint run gate via make":        "run_gate",
	"capture.Run executing command": "run_command",
	"lint run cpu command":          "run_command",
	"gate run token command":        "run_command",
	"lint resolve go env":           "resolve_env",
	"lint extract findings":         "extract",
	"notice read records":           "notice",
	"notice read applied":           "notice",
	"notice read seen":              "notice",
	"notice write seen":             "notice",
	"notice append applied":         "notice",
}

// fallbackPrefixes are stripped from an unknown message before it becomes a
// summary sentence, so "lint evaluate bypass token" reads as "Evaluate bypass
// token" rather than leaking the internal subsystem prefix.
var fallbackPrefixes = []string{"lint ", "gate ", "notice ", "capture.Run ", "staticcheck "}

// tally is one rendered summary line: its phrasing and accumulated count.
type tally struct {
	phrasing phrasing
	count    int
}

// classify maps a slog message to its bucket id and phrasing. Unknown messages
// get a stable id derived from the message and a sentence-cased fallback
// phrasing so a future boundary log still renders without a table entry.
func classify(message string) (string, phrasing) {
	if id, ok := messageBucket[message]; ok {
		return id, bucketPhrasing[id]
	}
	label := sentenceCase(stripPrefix(message))
	return "fallback:" + label, phrasing{
		singular: label + ": %d time",
		plural:   label + ": %d times",
	}
}

// stripPrefix removes a known subsystem prefix from message.
func stripPrefix(message string) string {
	for _, prefix := range fallbackPrefixes {
		if strings.HasPrefix(message, prefix) {
			return message[len(prefix):]
		}
	}
	return message
}

// sentenceCase uppercases the first rune of text and leaves the rest unchanged.
func sentenceCase(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// phrases collapses message counts into buckets and returns the rendered
// sentences ("Read 14 files", "Ran 9 commands", ...) ordered by count descending
// then text, so both the multi-line block and the one-line footnote share one
// stable ordering.
func phrases(counts map[string]int) []string {
	if len(counts) == 0 {
		return nil
	}
	tallies := make(map[string]*tally)
	for message, count := range counts {
		id, phrase := classify(message)
		existing, ok := tallies[id]
		if !ok {
			existing = &tally{phrasing: phrase}
			tallies[id] = existing
		}
		existing.count += count
	}

	lines := make([]string, 0, len(tallies))
	for _, item := range tallies {
		lines = append(lines, sentenceFor(item))
	}
	sortByCountThenText(tallies, lines)
	return lines
}

// OneLine renders the diagnostics counts as a single lowercase footnote such as
// "read 14 files, ran 9 commands, installed 5 Go tools", or the empty string
// when there is nothing to report. The report prints it under "Diagnostics:".
func OneLine(counts map[string]int) string {
	lines := phrases(counts)
	if len(lines) == 0 {
		return ""
	}
	lowered := make([]string, len(lines))
	for index, line := range lines {
		lowered[index] = lowerFirst(line)
	}
	return strings.Join(lowered, ", ")
}

// lowerFirst lowercases only the first rune so "Installed 5 Go tools" becomes
// "installed 5 Go tools" without touching the embedded "Go".
func lowerFirst(text string) string {
	if text == "" {
		return text
	}
	return strings.ToLower(text[:1]) + text[1:]
}

// sentenceFor renders one tally as its singular or plural sentence.
func sentenceFor(item *tally) string {
	form := item.phrasing.plural
	if item.count == 1 {
		form = item.phrasing.singular
	}
	return fmt.Sprintf(form, item.count)
}

// sortByCountThenText sorts lines by their tally count descending, then by the
// rendered text ascending, so the output is deterministic for tests and reads
// busiest first.
func sortByCountThenText(tallies map[string]*tally, lines []string) {
	countOf := make(map[string]int, len(lines))
	for _, item := range tallies {
		countOf[sentenceFor(item)] = item.count
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if countOf[lines[i]] != countOf[lines[j]] {
			return countOf[lines[i]] > countOf[lines[j]]
		}
		return lines[i] < lines[j]
	})
}
