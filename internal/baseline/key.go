// Package baseline rewrites lint baseline files and reports neutral update
// counts. It is the Go port of scripts/go-mk-baseline.awk and the baseline
// helpers in scripts/go-mk-common.sh. The shell still performs finding capture
// and the token gate; this package owns the baseline rewrite, the update
// statistics, and the rendered output.
package baseline

import "regexp"

// lineColPattern matches the first :LINE:COL: coordinate in a finding. It is the
// Go equivalent of the awk /:[0-9]+:[0-9]+:/ match, and RE2 leftmost matching
// reproduces awk's match() semantics.
var lineColPattern = regexp.MustCompile(`:[0-9]+:[0-9]+:`)

// stripDotDot removes leading "../" segments, mirroring the awk
// `while (index(line, "../") == 1)` loop. It operates on bytes, matching awk's
// byte-based index/substr.
func stripDotDot(line string) string {
	for len(line) >= 3 && line[:3] == "../" {
		line = line[3:]
	}
	return line
}

// collapseLineCol replaces the first :LINE:COL: coordinate with ":::" so a
// finding matches regardless of where it moves within a file. Mirrors the awk
// substr rewrite around the first match only.
func collapseLineCol(line string) string {
	loc := lineColPattern.FindStringIndex(line)
	if loc == nil {
		return line
	}
	return line[:loc[0]] + ":::" + line[loc[1]:]
}

// Key reduces a finding to its baseline key. Both go-mk-baseline.awk key_for and
// go-mk-findings.awk key_for collapse to this same operation here, because the
// keyize and baseline-extract awk invocations run without pwd/cwd (path
// normalization happens earlier, during shell capture). So one key function
// serves both the rewriter dedup and the statistics.
func Key(finding string) string {
	return collapseLineCol(stripDotDot(finding))
}
