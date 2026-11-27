// Package checkpolicy defines which quality findings allow a successful exit.
package checkpolicy

import "strings"

// An empty analyzer list makes all findings from the check advisory.
var advisory = map[string][]string{
	"deadcode":      nil,
	"go-version":    nil,
	"govulncheck":   nil,
	"golangci-lint": {"unused"},
}

// AdvisoryCheck reports whether every finding from a check is advisory.
//
// Query this policy for tools that report findings without analyzer labels.
//
//	advisory := AdvisoryCheck("go-version")
func AdvisoryCheck(check string) bool {
	analyzers, ok := advisory[check]

	return ok && len(analyzers) == 0
}

// AdvisoryFinding reports whether a check allows a finding without failing.
//
// Golangci-lint adds the analyzer name in parentheses after its message.
//
//	advisory := AdvisoryFinding("golangci-lint", "func run is unused (unused)")
func AdvisoryFinding(check, finding string) bool {
	if AdvisoryCheck(check) {
		return true
	}

	for _, analyzer := range advisory[check] {
		if strings.HasSuffix(finding, " ("+analyzer+")") {
			return true
		}
	}

	return false
}
