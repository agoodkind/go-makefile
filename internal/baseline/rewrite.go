package baseline

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Mode is a baseline update mode. The user-facing output never names a mode;
// the mode only selects which rows the rewriter keeps.
type Mode int

const (
	// ModeSync records every current finding and drops fixed in-scope rows.
	ModeSync Mode = iota
	// ModePruneFixed keeps only current findings already in the baseline.
	ModePruneFixed
	// ModeAcceptNew records every current finding and keeps every old row.
	ModeAcceptNew
)

// modeByName maps each accepted mode string to its Mode. "remove-fixed" is an
// alias for "prune-fixed", matching normalize_mode in go-mk-baseline.sh and the
// awk dispatch.
var modeByName = map[string]Mode{
	"sync":         ModeSync,
	"prune-fixed":  ModePruneFixed,
	"remove-fixed": ModePruneFixed,
	"accept-new":   ModeAcceptNew,
}

// ParseMode maps a mode string to a Mode. The error text names no maintenance
// action.
func ParseMode(value string) (Mode, error) {
	if mode, ok := modeByName[value]; ok {
		return mode, nil
	}
	return 0, errors.New("unknown baseline update mode: " + value)
}

// isBlank reports whether a line is empty or only spaces and tabs, matching the
// awk /^[ \t]*$/ test.
func isBlank(line string) bool {
	for index := 0; index < len(line); index++ {
		if line[index] != ' ' && line[index] != '\t' {
			return false
		}
	}
	return true
}

// skipInput reports whether an input line is ignored on read, matching the awk
// `line ~ /^[ \t]*$/ || line ~ /^#/` guard.
func skipInput(line string) bool {
	return isBlank(line) || strings.HasPrefix(line, "#")
}

// firstAddedFrom extracts the first_added value from a metadata suffix,
// mirroring first_added_from in go-mk-baseline.awk (split on whitespace, take
// the field after "first_added=").
func firstAddedFrom(metadata string) string {
	for _, field := range strings.Fields(metadata) {
		if strings.HasPrefix(field, "first_added=") {
			return field[len("first_added="):]
		}
	}
	return ""
}

// RewriteInput holds everything the rewriter needs for one baseline.
type RewriteInput struct {
	CurrentLines []string
	OldLines     []string
	Label        string
	Now          string
	ScopePattern string
	Mode         Mode
}

// RewriteBody reproduces go-mk-baseline.awk byte-for-byte: it returns the
// baseline body lines (without the generated_at header), in insertion order,
// preserving first_added metadata and out-of-scope rows. The caller writes the
// header and joins the body with newlines.
func RewriteBody(input RewriteInput) ([]string, error) {
	var scopeRegexp *regexp.Regexp
	if input.ScopePattern != "" {
		compiled, err := regexp.Compile(input.ScopePattern)
		if err != nil {
			return nil, err
		}
		scopeRegexp = compiled
	}

	var currentOrder []string
	currentFinding := map[string]string{}
	for _, line := range input.CurrentLines {
		if skipInput(line) {
			continue
		}
		key := Key(line)
		if _, seen := currentFinding[key]; !seen {
			currentOrder = append(currentOrder, key)
		}
		currentFinding[key] = line
	}

	marker := "\t# " + input.Label + ":"
	var oldOrder []string
	oldFinding := map[string]string{}
	oldLine := map[string]string{}
	oldFirstAdded := map[string]string{}
	for _, line := range input.OldLines {
		if skipInput(line) {
			continue
		}
		finding := line
		metadata := ""
		if markerIndex := strings.Index(line, marker); markerIndex >= 0 {
			finding = line[:markerIndex]
			metadata = line[markerIndex+len(marker):]
		}
		if finding == "" {
			continue
		}
		key := Key(finding)
		if _, seen := oldFinding[key]; !seen {
			oldOrder = append(oldOrder, key)
		}
		oldFinding[key] = finding
		oldLine[key] = line
		oldFirstAdded[key] = firstAddedFrom(metadata)
	}

	renderCurrent := func(key string) string {
		firstAdded := oldFirstAdded[key]
		if firstAdded == "" {
			firstAdded = input.Now
		}
		return fmt.Sprintf(
			"%s\t# %s:first_added=%s last_seen=%s",
			currentFinding[key], input.Label, firstAdded, input.Now)
	}

	inScope := func(finding string) bool {
		if scopeRegexp == nil {
			return true
		}
		return scopeRegexp.MatchString(finding)
	}

	oldOutsideScope := func() []string {
		if scopeRegexp == nil {
			return nil
		}
		var rows []string
		for _, key := range oldOrder {
			_, inCurrent := currentFinding[key]
			if !inCurrent && !inScope(oldFinding[key]) {
				rows = append(rows, oldLine[key])
			}
		}
		return rows
	}

	var out []string
	switch input.Mode {
	case ModeSync:
		for _, key := range currentOrder {
			out = append(out, renderCurrent(key))
		}
		out = append(out, oldOutsideScope()...)
	case ModePruneFixed:
		for _, key := range currentOrder {
			if _, inOld := oldFinding[key]; inOld {
				out = append(out, renderCurrent(key))
			}
		}
		out = append(out, oldOutsideScope()...)
	case ModeAcceptNew:
		for _, key := range currentOrder {
			out = append(out, renderCurrent(key))
		}
		for _, key := range oldOrder {
			if _, inCurrent := currentFinding[key]; !inCurrent {
				out = append(out, oldLine[key])
			}
		}
	}
	return out, nil
}

// RenderFile assembles the full baseline file contents: the generated_at header
// followed by the rewritten body, matching go_mk_write_baseline_file.
func RenderFile(title, now string, body []string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s: generated_at=%s\n", title, now)
	for _, line := range body {
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return builder.String()
}
