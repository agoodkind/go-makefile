// Package gate ports the pure decision logic of scripts/go-mk-gate.sh into Go:
// whether a confirm value is affirmative and whether two tokens match after
// slugification. The package never runs the token command, reads, or writes the
// stamp file; the command layer in cmd/go-mk owns those side effects, mirroring
// the internal/findings, internal/capture, internal/lintgate, and internal/lint
// split.
//
// =============================================================================
// gate
// =============================================================================
package gate

import "goodkind.io/go-makefile/internal/lint"

// affirmativeConfirmValues is the set of confirm values the shell case arm
// accepts (1, y, yes, Y, YES). It is a set rather than a switch so the
// bare-string-switch analyzer stays satisfied while preserving the exact values.
var affirmativeConfirmValues = map[string]struct{}{
	"1":   {},
	"y":   {},
	"yes": {},
	"Y":   {},
	"YES": {},
}

// ConfirmAccepted reports whether the confirm value is one of the affirmative
// values the shell case arm accepts, mirroring the go-mk-gate.sh confirm_value
// case that otherwise exits without writing the stamp.
//
// ---- ConfirmAccepted ----
func ConfirmAccepted(confirmValue string) bool {
	_, ok := affirmativeConfirmValues[confirmValue]
	return ok
}

// TokensMatch reports whether the expected and actual tokens are equal after
// slugification, mirroring the go-mk-gate.sh comparison: both are slugified, and
// a match requires both to be non-empty and identical. An empty expected or
// actual token never matches, so a failed token command (empty output) cannot
// open the gate.
//
// ---- TokensMatch ----
func TokensMatch(expectedRaw, actualRaw string) bool {
	expected := lint.Slugify(expectedRaw)
	actual := lint.Slugify(actualRaw)
	if expected == "" || actual == "" {
		return false
	}
	return expected == actual
}
