package findings

import "regexp"

var (
	nilErrorLinePattern = regexp.MustCompile(
		`\((line [0-9]+|lines \[[0-9]+(?: [0-9]+)*\])\) but it returns (nil|error) \(nilerr\)$`,
	)
	lineNumberPattern = regexp.MustCompile(`[0-9]+`)
)

// SourceLocationKey normalizes embedded line references in nilerr reports.
//
// The nilerr analyzer repeats source line numbers in its diagnostic messages.
// Other diagnostics retain their complete message.
//
// For example, the line reference does not identify a new finding:
//
//	error is not nil (line 60) but it returns nil (nilerr)
func SourceLocationKey(diagnostic string) string {
	return nilErrorLinePattern.ReplaceAllStringFunc(
		diagnostic,
		func(location string) string {
			return lineNumberPattern.ReplaceAllString(location, "<source>")
		},
	)
}
