package findings

import "regexp"

var nilErrorLinePattern = regexp.MustCompile(`\(line [0-9]+\) but it returns nil \(nilerr\)$`)

// SourceLocationKey normalizes embedded source line references in nilerr findings.
//
// The nilerr analyzer repeats a source line number in its diagnostic message.
// Other diagnostics retain their complete message.
//
// For example, the line reference does not identify a new finding:
//
//	error is not nil (line 60) but it returns nil (nilerr)
func SourceLocationKey(diagnostic string) string {
	return nilErrorLinePattern.ReplaceAllString(
		diagnostic,
		"(line <source>) but it returns nil (nilerr)",
	)
}
