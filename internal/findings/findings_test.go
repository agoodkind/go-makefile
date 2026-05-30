package findings

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// findingsAwkPath resolves the awk script under test relative to this file,
// mirroring how baseline_test.go locates go-mk-baseline.awk.
func findingsAwkPath(t *testing.T) string {
	t.Helper()
	scriptPath, err := filepath.Abs("../../scripts/go-mk-findings.awk")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(scriptPath); statErr != nil {
		t.Skipf("awk script not found: %v", statErr)
	}
	return scriptPath
}

// lookAwk returns the awk binary path or skips when awk is unavailable, matching
// the baseline oracle's clean skip.
func lookAwk(t *testing.T) string {
	t.Helper()
	awkPath, err := exec.LookPath("awk")
	if err != nil {
		t.Skip("awk not available")
	}
	return awkPath
}

// writeTempFile writes content to a uniquely named file in the test temp dir and
// returns its path, used to feed the awk positional file arguments.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runAwk runs the awk script with the given -v assignments, positional file
// arguments, and stdin, returning stdout. A non-zero awk exit fails the test.
func runAwk(t *testing.T, awkPath, scriptPath string, assigns, files []string, stdin string) string {
	t.Helper()
	args := make([]string, 0, len(assigns)*2+len(files)+2)
	for _, assign := range assigns {
		args = append(args, "-v", assign)
	}
	args = append(args, "-f", scriptPath)
	args = append(args, files...)
	command := exec.Command(awkPath, args...)
	command.Stdin = strings.NewReader(stdin)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("awk run (assigns=%v files=%v): %v", assigns, files, err)
	}
	return string(output)
}

// goJoin mirrors the command layer's line joining: it terminates each line with
// a newline and yields the empty string for no lines, matching awk print so the
// oracle compares the package output against awk byte-for-byte.
func goJoin(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// goMap applies transform to every line, used to drive the per-line transforms
// against the same fixture slice the awk reads.
func goMap(lines []string, transform func(string) string) []string {
	out := make([]string, len(lines))
	for index, line := range lines {
		out[index] = transform(line)
	}
	return out
}

// The fixtures deliberately exercise the tricky cases the awk normalize_path,
// key_for, baseline_finding, print_finding, ranges, linefilter, and map handle.
const (
	fixturePwd = "/work/repo/"
	fixtureCwd = "/work/repo/sub/"
	// pwdPrefixOfCwd uses a pwd that is a string prefix of cwd, so the awk
	// strips pwd first and cwd second from the same line.
	pwdPrefixOfCwd = "/work/"
)

// normalizeFixtures covers a normal finding, a pwd-prefixed path, a cwd-prefixed
// path, leading ../ segments stacked twice and once, a no-colon line, and a path
// that starts with neither prefix.
var normalizeFixtures = []string{
	"pkg/file.go:10:2: something wrong (linter)",
	"/work/repo/pkg/file.go:10:2: prefixed by pwd",
	"/work/repo/sub/pkg/file.go:10:2: prefixed by cwd",
	"../../pkg/file.go:10:2: leading dotdot pair",
	"../pkg/file.go:7:1: single dotdot",
	"a line with no colon at all",
	"unrelated/path.go:3:4: neither prefix here",
}

func TestFindingsNormalizeMatchesAwk(t *testing.T) {
	awkPath := lookAwk(t)
	scriptPath := findingsAwkPath(t)
	input := strings.Join(normalizeFixtures, "\n") + "\n"
	inputFile := writeTempFile(t, "normalize.in", input)
	for _, pwd := range []string{"", fixturePwd, pwdPrefixOfCwd} {
		for _, cwd := range []string{"", fixtureCwd} {
			want := runAwk(t, awkPath, scriptPath,
				[]string{"action=normalize", "pwd=" + pwd, "cwd=" + cwd},
				[]string{inputFile}, "")
			got := goJoin(goMap(normalizeFixtures, func(line string) string {
				return NormalizePath(line, pwd, cwd)
			}))
			if got != want {
				t.Errorf("normalize pwd=%q cwd=%q\n--- go ---\n%q\n--- awk ---\n%q", pwd, cwd, got, want)
			}
		}
	}
}

func TestFindingsKeyMatchesAwk(t *testing.T) {
	awkPath := lookAwk(t)
	scriptPath := findingsAwkPath(t)
	input := strings.Join(normalizeFixtures, "\n") + "\n"
	inputFile := writeTempFile(t, "key.in", input)
	for _, pwd := range []string{"", fixturePwd} {
		for _, cwd := range []string{"", fixtureCwd} {
			want := runAwk(t, awkPath, scriptPath,
				[]string{"action=key", "pwd=" + pwd, "cwd=" + cwd},
				[]string{inputFile}, "")
			got := goJoin(goMap(normalizeFixtures, func(line string) string {
				return Key(line, pwd, cwd)
			}))
			if got != want {
				t.Errorf("key pwd=%q cwd=%q\n--- go ---\n%q\n--- awk ---\n%q", pwd, cwd, got, want)
			}
		}
	}
}

// baselineFixtures include a normal labeled row, a row without the marker, a
// blank line, a whitespace-only line, a comment line, and a pwd-prefixed row.
var baselineFixtures = []string{
	"pkg/file.go:10:2: kept finding\t# sample:first_added=X last_seen=Y",
	"pkg/other.go:1:1: no marker on this row",
	"",
	"   \t ",
	"# a comment line",
	"/work/repo/pkg/p.go:5:6: pwd prefixed\t# sample:first_added=X last_seen=Y",
}

func TestFindingsBaselineMatchesAwk(t *testing.T) {
	awkPath := lookAwk(t)
	scriptPath := findingsAwkPath(t)
	input := strings.Join(baselineFixtures, "\n") + "\n"
	inputFile := writeTempFile(t, "baseline.in", input)
	const label = "sample"
	for _, pwd := range []string{"", fixturePwd} {
		want := runAwk(t, awkPath, scriptPath,
			[]string{"action=baseline", "label=" + label, "pwd=" + pwd, "cwd="},
			[]string{inputFile}, "")
		kept := make([]string, 0, len(baselineFixtures))
		for _, line := range baselineFixtures {
			payload, ok := Baseline(line, label, pwd, "")
			if !ok {
				continue
			}
			kept = append(kept, payload)
		}
		got := goJoin(kept)
		if got != want {
			t.Errorf("baseline pwd=%q\n--- go ---\n%q\n--- awk ---\n%q", pwd, got, want)
		}
	}
}

func TestFindingsPrintMatchesAwk(t *testing.T) {
	awkPath := lookAwk(t)
	scriptPath := findingsAwkPath(t)
	input := strings.Join(normalizeFixtures, "\n") + "\n"
	for _, pwd := range []string{"", fixturePwd} {
		for _, cwd := range []string{"", fixtureCwd} {
			want := runAwk(t, awkPath, scriptPath,
				[]string{"action=print", "pwd=" + pwd, "cwd=" + cwd},
				nil, input)
			var builder strings.Builder
			for _, line := range normalizeFixtures {
				builder.WriteString(Print(line, pwd, cwd))
			}
			got := builder.String()
			if got != want {
				t.Errorf("print pwd=%q cwd=%q\n--- go ---\n%q\n--- awk ---\n%q", pwd, cwd, got, want)
			}
		}
	}
}

// mapFindings are the raw finding lines fed on stdin for the map action. Some
// keys match the saved-key set and some do not.
var mapFindings = []string{
	"pkg/file.go:10:2: in the saved set",
	"pkg/file.go:99:5: same path different coords still in set",
	"pkg/other.go:3:4: not in the saved set",
	"no colon line in set",
	"no colon line not in set",
}

func TestFindingsMapMatchesAwk(t *testing.T) {
	awkPath := lookAwk(t)
	scriptPath := findingsAwkPath(t)
	// The saved-key file holds key_for output, so a path:::message entry
	// matches every finding on that path regardless of line:col, plus a
	// no-colon key.
	savedKeys := []string{
		"pkg/file.go::: in the saved set",
		"no colon line in set",
	}
	keysFile := writeTempFile(t, "keys.txt", strings.Join(savedKeys, "\n")+"\n")
	findingsFile := writeTempFile(t, "findings.txt", strings.Join(mapFindings, "\n")+"\n")
	want := runAwk(t, awkPath, scriptPath,
		[]string{"action=map"},
		[]string{keysFile, findingsFile}, "")
	savedSet := make(map[string]struct{}, len(savedKeys))
	for _, key := range savedKeys {
		savedSet[key] = struct{}{}
	}
	got := goJoin(Map(mapFindings, savedSet, "", ""))
	if got != want {
		t.Errorf("map\n--- go ---\n%q\n--- awk ---\n%q", got, want)
	}
}

// diffFixture is a small unified diff exercising the ranges action: a normal
// hunk with a count, a single-line hunk with no count, and a /dev/null target
// that must be ignored.
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

func TestFindingsRangesMatchesAwk(t *testing.T) {
	awkPath := lookAwk(t)
	scriptPath := findingsAwkPath(t)
	patchFile := writeTempFile(t, "patch.diff", diffFixture)
	want := runAwk(t, awkPath, scriptPath,
		[]string{"action=ranges"},
		[]string{patchFile}, "")
	rows := make([]string, 0)
	for _, span := range Ranges(strings.Split(strings.TrimRight(diffFixture, "\n"), "\n")) {
		rows = append(rows, span.File+"\t"+strconv.Itoa(span.Start)+"\t"+strconv.Itoa(span.End))
	}
	got := goJoin(rows)
	if got != want {
		t.Errorf("ranges\n--- go ---\n%q\n--- awk ---\n%q", got, want)
	}
}

// lineFilterFindings feed stdin for the linefilter action; some fall inside a
// recorded range and some do not, and one has a non-numeric line field.
var lineFilterFindings = []string{
	"pkg/file.go:11:2: inside the first range",
	"pkg/file.go:30:1: inside the single-line range",
	"pkg/file.go:99:1: outside any range",
	"pkg/other.go:11:1: right line wrong file",
	"pkg/file.go:notanumber: non-numeric line",
}

func TestFindingsLineFilterMatchesAwk(t *testing.T) {
	awkPath := lookAwk(t)
	scriptPath := findingsAwkPath(t)
	rangeRows := []string{
		"pkg/file.go\t10\t13",
		"pkg/file.go\t30\t30",
	}
	rangesFile := writeTempFile(t, "ranges.txt", strings.Join(rangeRows, "\n")+"\n")
	input := strings.Join(lineFilterFindings, "\n") + "\n"
	want := runAwk(t, awkPath, scriptPath,
		[]string{"action=linefilter"},
		[]string{rangesFile, "-"}, input)
	parsedRanges := make([]Range, 0, len(rangeRows))
	for _, row := range rangeRows {
		columns := strings.Split(row, "\t")
		start, _ := strconv.Atoi(columns[1])
		end, _ := strconv.Atoi(columns[2])
		parsedRanges = append(parsedRanges, Range{File: columns[0], Start: start, End: end})
	}
	got := goJoin(LineFilter(lineFilterFindings, parsedRanges))
	if got != want {
		t.Errorf("linefilter\n--- go ---\n%q\n--- awk ---\n%q", got, want)
	}
}

// TestFindingsMatchesAwk is the umbrella oracle entry point named in the task. It
// runs every per-action oracle as a subtest so a single name covers the suite
// and a missing awk skips cleanly.
func TestFindingsMatchesAwk(t *testing.T) {
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("awk not available")
	}
	t.Run("normalize", TestFindingsNormalizeMatchesAwk)
	t.Run("key", TestFindingsKeyMatchesAwk)
	t.Run("baseline", TestFindingsBaselineMatchesAwk)
	t.Run("print", TestFindingsPrintMatchesAwk)
	t.Run("map", TestFindingsMapMatchesAwk)
	t.Run("ranges", TestFindingsRangesMatchesAwk)
	t.Run("linefilter", TestFindingsLineFilterMatchesAwk)
}
