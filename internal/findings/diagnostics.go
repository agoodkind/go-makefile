package findings

import (
	"regexp"
	"strings"
)

var (
	diagnosticStart = regexp.MustCompile(`^[^[:space:]][^:]+:[0-9]+(?::[0-9]+)?:[ \t]`)
	diagnosticEnd   = regexp.MustCompile(`\([[:alnum:]_-]+\)$`)
)

// JoinDiagnosticLines combines multiline messages with their source locations.
//
// A diagnostic starts with a file location and ends with a linter tag.
// Source excerpts and incomplete messages retain their original lines.
//
// Example:
//
//	JoinDiagnosticLines([]string{
//		"example.go:3:2: prefer an integer range",
//		"Check the loop limit. (intrange)",
//	})
func JoinDiagnosticLines(lines []string) []string {
	joined := make([]string, 0, len(lines))

	var pending []string

	for _, line := range lines {
		starts := diagnosticStart.MatchString(line)
		if starts {
			joined = append(joined, pending...)
			pending = nil
		}

		if !starts && len(pending) == 0 {
			joined = append(joined, line)

			continue
		}

		pending = append(pending, line)
		if diagnosticEnd.MatchString(line) {
			joined = append(joined, strings.Join(pending, " "))
			pending = nil
		}
	}

	return append(joined, pending...)
}
