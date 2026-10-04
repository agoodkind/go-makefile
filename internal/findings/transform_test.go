package findings_test

import (
	"reflect"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/findings"
)

const (
	fixturePwd = "/work/repo/"
	fixtureCwd = "/work/repo/sub/"
	// pwdPrefixOfCwd is a string prefix of fixtureCwd. One line can match the
	// pwd strip and then the cwd strip.
	pwdPrefixOfCwd = "/work/"
)

// inputLines covers a normal finding, a pwd-prefixed path, a cwd-prefixed path,
// leading ../ segments stacked twice and once, a line with no colon, and a path
// that starts with neither prefix.
var inputLines = []string{
	"pkg/file.go:10:2: something wrong (linter)",
	"/work/repo/pkg/file.go:10:2: prefixed by pwd",
	"/work/repo/sub/pkg/file.go:10:2: prefixed by cwd",
	"../../pkg/file.go:10:2: leading dotdot pair",
	"../pkg/file.go:7:1: single dotdot",
	"a line with no colon at all",
	"unrelated/path.go:3:4: neither prefix here",
}

type prefixCase struct {
	name string
	pwd  string
	cwd  string
	want []string
}

func mapLines(lines []string, transform func(string) string) []string {
	result := make([]string, len(lines))
	for index, line := range lines {
		result[index] = transform(line)
	}
	return result
}

func TestNormalizePath(t *testing.T) {
	cases := []prefixCase{
		{"no prefixes", "", "", []string{
			"pkg/file.go:10:2: something wrong (linter)",
			"/work/repo/pkg/file.go:10:2: prefixed by pwd",
			"/work/repo/sub/pkg/file.go:10:2: prefixed by cwd",
			"pkg/file.go:10:2: leading dotdot pair",
			"pkg/file.go:7:1: single dotdot",
			"a line with no colon at all",
			"unrelated/path.go:3:4: neither prefix here",
		}},
		{"cwd only", "", fixtureCwd, []string{
			"pkg/file.go:10:2: something wrong (linter)",
			"/work/repo/pkg/file.go:10:2: prefixed by pwd",
			"pkg/file.go:10:2: prefixed by cwd",
			"pkg/file.go:10:2: leading dotdot pair",
			"pkg/file.go:7:1: single dotdot",
			"a line with no colon at all",
			"unrelated/path.go:3:4: neither prefix here",
		}},
		{"pwd only", fixturePwd, "", []string{
			"pkg/file.go:10:2: something wrong (linter)",
			"pkg/file.go:10:2: prefixed by pwd",
			"sub/pkg/file.go:10:2: prefixed by cwd",
			"pkg/file.go:10:2: leading dotdot pair",
			"pkg/file.go:7:1: single dotdot",
			"a line with no colon at all",
			"unrelated/path.go:3:4: neither prefix here",
		}},
		{"pwd prefix of cwd, with cwd", pwdPrefixOfCwd, fixtureCwd, []string{
			"pkg/file.go:10:2: something wrong (linter)",
			"repo/pkg/file.go:10:2: prefixed by pwd",
			"repo/sub/pkg/file.go:10:2: prefixed by cwd",
			"pkg/file.go:10:2: leading dotdot pair",
			"pkg/file.go:7:1: single dotdot",
			"a line with no colon at all",
			"unrelated/path.go:3:4: neither prefix here",
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := mapLines(inputLines, func(line string) string {
				return findings.NormalizePath(line, testCase.pwd, testCase.cwd)
			})
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("NormalizePath pwd=%q cwd=%q\ngot:  %q\nwant: %q",
					testCase.pwd, testCase.cwd, got, testCase.want)
			}
		})
	}
}

// TestKey covers the :line:col: collapse after path normalization.
// TestNormalizePath covers the prefix combinations.
func TestKey(t *testing.T) {
	want := []string{
		"pkg/file.go::: something wrong (linter)",
		"pkg/file.go::: prefixed by pwd",
		"sub/pkg/file.go::: prefixed by cwd",
		"pkg/file.go::: leading dotdot pair",
		"pkg/file.go::: single dotdot",
		"a line with no colon at all",
		"unrelated/path.go::: neither prefix here",
	}
	got := mapLines(inputLines, func(line string) string {
		return findings.Key(line, fixturePwd, "")
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Key\ngot:  %q\nwant: %q", got, want)
	}
}

// TestPrint covers the two-line display form and the single-line form for a
// finding with no location.
func TestPrint(t *testing.T) {
	want := "  pkg/file.go:10:2\n    something wrong (linter)\n" +
		"  pkg/file.go:10:2\n    prefixed by pwd\n" +
		"  sub/pkg/file.go:10:2\n    prefixed by cwd\n" +
		"  pkg/file.go:10:2\n    leading dotdot pair\n" +
		"  pkg/file.go:7:1\n    single dotdot\n" +
		"  a line with no colon at all\n" +
		"  unrelated/path.go:3:4\n    neither prefix here\n"
	var builder strings.Builder
	for _, line := range inputLines {
		builder.WriteString(findings.Print(line, fixturePwd, ""))
	}
	if got := builder.String(); got != want {
		t.Errorf("Print\ngot:  %q\nwant: %q", got, want)
	}
}

// baselineLines include a labeled row, a row without the marker, a blank line, a
// whitespace-only line, a comment line, and a pwd-prefixed labeled row.
var baselineLines = []string{
	"pkg/file.go:10:2: kept finding\t# sample:first_added=X last_seen=Y",
	"pkg/other.go:1:1: no marker on this row",
	"",
	"   \t ",
	"# a comment line",
	"/work/repo/pkg/p.go:5:6: pwd prefixed\t# sample:first_added=X last_seen=Y",
}

func TestBaseline(t *testing.T) {
	cases := []prefixCase{
		{"no pwd", "", "", []string{
			"pkg/file.go:10:2: kept finding",
			"pkg/other.go:1:1: no marker on this row",
			"/work/repo/pkg/p.go:5:6: pwd prefixed",
		}},
		{"pwd stripped", fixturePwd, "", []string{
			"pkg/file.go:10:2: kept finding",
			"pkg/other.go:1:1: no marker on this row",
			"pkg/p.go:5:6: pwd prefixed",
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := make([]string, 0, len(baselineLines))
			for _, line := range baselineLines {
				payload, ok := findings.Baseline(line, "sample", testCase.pwd, testCase.cwd)
				if !ok {
					continue
				}
				got = append(got, payload)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("Baseline pwd=%q\ngot:  %q\nwant: %q", testCase.pwd, got, testCase.want)
			}
		})
	}
}

func TestMap(t *testing.T) {
	rawFindings := []string{
		"pkg/file.go:10:2: in the saved set",
		"pkg/file.go:99:5: same path different coords still in set",
		"pkg/other.go:3:4: not in the saved set",
		"no colon line in set",
		"no colon line not in set",
	}
	// Saved keys use the Key format. The first entry matches the finding on
	// pkg/file.go with that message at any line and column.
	savedSet := map[string]struct{}{
		"pkg/file.go::: in the saved set": {},
		"no colon line in set":            {},
	}
	want := []string{
		"pkg/file.go:10:2: in the saved set",
		"no colon line in set",
	}
	got := findings.Map(rawFindings, savedSet, "", "")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Map\ngot:  %q\nwant: %q", got, want)
	}
}

const diffFixture = `diff --git a/pkg/file.go b/pkg/file.go
--- a/pkg/file.go
+++ b/pkg/file.go
@@ -1,3 +10,4 @@ func Foo() {
 context
+added
+added
@@ -20 +30 @@ func Bar() {
+single
diff --git a/gone.go b/gone.go
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-removed
-removed
`

func TestRanges(t *testing.T) {
	diffLines := strings.Split(strings.TrimRight(diffFixture, "\n"), "\n")
	want := []findings.Range{
		{File: "pkg/file.go", Start: 10, End: 13},
		{File: "pkg/file.go", Start: 30, End: 30},
	}
	got := findings.Ranges(diffLines)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ranges\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestLineFilter(t *testing.T) {
	rawFindings := []string{
		"pkg/file.go:11:2: inside the first range",
		"pkg/file.go:30:1: inside the single-line range",
		"pkg/file.go:99:1: outside any range",
		"pkg/other.go:11:1: right line wrong file",
		"pkg/file.go:notanumber: non-numeric line",
	}
	ranges := []findings.Range{
		{File: "pkg/file.go", Start: 10, End: 13},
		{File: "pkg/file.go", Start: 30, End: 30},
	}
	want := []string{
		"pkg/file.go:11:2: inside the first range",
		"pkg/file.go:30:1: inside the single-line range",
	}
	got := findings.LineFilter(rawFindings, ranges)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LineFilter\ngot:  %q\nwant: %q", got, want)
	}
}
