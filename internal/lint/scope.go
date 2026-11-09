package lint

import "regexp"

// ScopeLines retains diagnostics that match a compiled scope.
//
// A nil scope retains all input lines. Selection preserves the input order.
//
// Apply a compiled diagnostic selector with:
//
//	selected := ScopeLines(lines, scope)
func ScopeLines(lines []string, scope *regexp.Regexp) []string {
	if scope == nil {
		return lines
	}

	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if scope.MatchString(line) {
			kept = append(kept, line)
		}
	}

	return kept
}
