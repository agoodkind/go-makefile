package baseline

import (
	"regexp"
	"strings"
)

// baselineFinding extracts the finding portion of a baseline line, stripping the
// metadata marker, mirroring baseline_finding in go-mk-findings.awk. Blank and
// comment lines yield "". Path normalization here is the leading-"../" strip
// only, because the baseline-extract awk runs without pwd/cwd.
func baselineFinding(line, label string) string {
	if skipInput(line) {
		return ""
	}
	finding := line
	marker := "\t# " + label + ":"
	if markerIndex := strings.Index(line, marker); markerIndex >= 0 {
		finding = line[:markerIndex]
	}
	return stripDotDot(finding)
}

// compileFilter compiles an extended-regex filter pattern (the grep -E form the
// shell uses for exclude and scope). An empty pattern compiles to nil, meaning
// "no filter".
func compileFilter(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return compiled, nil
}

// baselineKeySet reproduces go_mk_baseline_findings followed by go_mk_keyize_file
// for one baseline: extract findings, drop excluded rows (grep -Ev), keep scoped
// rows (grep -E), then reduce to the deduplicated key set. The returned set is
// what the statistics compare before and after a rewrite.
func baselineKeySet(
	baselineLines []string, label, excludePattern, scopePattern string,
) (map[string]struct{}, error) {
	exclude, err := compileFilter(excludePattern)
	if err != nil {
		return nil, err
	}
	scope, err := compileFilter(scopePattern)
	if err != nil {
		return nil, err
	}
	keys := map[string]struct{}{}
	for _, line := range baselineLines {
		finding := baselineFinding(line, label)
		if finding == "" {
			continue
		}
		if exclude != nil && exclude.MatchString(finding) {
			continue
		}
		if scope != nil && !scope.MatchString(finding) {
			continue
		}
		keys[Key(finding)] = struct{}{}
	}
	return keys, nil
}

// findingKeyList returns the key for every raw finding line, preserving
// duplicates and order, mirroring keyize without the sort -u dedup. Callers that
// need a set deduplicate themselves; callers that count covered findings keep
// the per-line view.
func findingKeyList(findingsLines []string) []string {
	keys := make([]string, 0, len(findingsLines))
	for _, line := range findingsLines {
		if isBlank(line) {
			continue
		}
		keys = append(keys, Key(line))
	}
	return keys
}
