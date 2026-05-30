package baseline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyStripsDotDotAndCollapsesLineCol(t *testing.T) {
	cases := map[string]string{
		"a.go:10:2: msg":           "a.go::: msg",
		"../../a.go:10:2: msg":     "a.go::: msg",
		"pkg/a.go:1:1: x (linter)": "pkg/a.go::: x (linter)",
		"no-coordinate line":       "no-coordinate line",
	}
	for input, want := range cases {
		if got := Key(input); got != want {
			t.Errorf("Key(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseModeAcceptsRemoveFixedAlias(t *testing.T) {
	for _, value := range []string{"prune-fixed", "remove-fixed"} {
		mode, err := ParseMode(value)
		if err != nil {
			t.Fatalf("ParseMode(%q) error: %v", value, err)
		}
		if mode != ModePruneFixed {
			t.Errorf("ParseMode(%q) = %v, want ModePruneFixed", value, mode)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Error("ParseMode(bogus) expected error")
	}
}

const oldBaseline = `# sample: generated_at=2026-01-01T00:00:00Z
old.go:10:2: fixed finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
keep.go:20:2: existing finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z`

const currentFindings = `keep.go:21:2: existing finding
new.go:30:2: new finding`

const oldScopedBaseline = `# sample: generated_at=2026-01-01T00:00:00Z
old-scoped.go:10:2: fixed scoped_rule finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
old-other.go:11:2: unrelated saved finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
keep-scoped.go:20:2: existing scoped_rule finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z`

const currentScopedFindings = `keep-scoped.go:21:2: existing scoped_rule finding
new-scoped.go:30:2: new scoped_rule finding`

func bodyFor(t *testing.T, oldText, currentText, mode, scope string) []string {
	t.Helper()
	parsedMode, err := ParseMode(mode)
	if err != nil {
		t.Fatalf("ParseMode(%q): %v", mode, err)
	}
	body, err := RewriteBody(RewriteInput{
		CurrentLines: strings.Split(currentText, "\n"),
		OldLines:     strings.Split(oldText, "\n"),
		Label:        "sample",
		Now:          "NOW",
		ScopePattern: scope,
		Mode:         parsedMode,
	})
	if err != nil {
		t.Fatalf("RewriteBody: %v", err)
	}
	return body
}

func TestRewriteSyncDropsFixedKeepsExistingAndNew(t *testing.T) {
	body := strings.Join(bodyFor(t, oldBaseline, currentFindings, "sync", ""), "\n")
	if !strings.Contains(body, "existing finding") {
		t.Error("sync should keep existing finding")
	}
	if !strings.Contains(body, "new finding") {
		t.Error("sync should add new finding")
	}
	if strings.Contains(body, "fixed finding") {
		t.Error("sync should drop fixed finding")
	}
	// first_added carried over for the surviving existing finding.
	if !strings.Contains(body, "keep.go:21:2: existing finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=NOW") {
		t.Errorf("sync did not carry first_added for existing finding:\n%s", body)
	}
	// new finding stamped with now for both timestamps.
	if !strings.Contains(body, "new.go:30:2: new finding\t# sample:first_added=NOW last_seen=NOW") {
		t.Errorf("sync did not stamp new finding:\n%s", body)
	}
}

func TestRewritePruneFixedDropsNewAndFixed(t *testing.T) {
	body := strings.Join(bodyFor(t, oldBaseline, currentFindings, "prune-fixed", ""), "\n")
	if !strings.Contains(body, "existing finding") {
		t.Error("prune-fixed should keep existing finding")
	}
	if strings.Contains(body, "new finding") {
		t.Error("prune-fixed should not add new finding")
	}
	if strings.Contains(body, "fixed finding") {
		t.Error("prune-fixed should drop fixed finding")
	}
}

func TestRewriteAcceptNewKeepsAllThree(t *testing.T) {
	body := strings.Join(bodyFor(t, oldBaseline, currentFindings, "accept-new", ""), "\n")
	for _, want := range []string{"existing finding", "new finding", "fixed finding"} {
		if !strings.Contains(body, want) {
			t.Errorf("accept-new should keep %q:\n%s", want, body)
		}
	}
}

func TestRewriteScopedPreservesOutOfScopeRow(t *testing.T) {
	body := strings.Join(bodyFor(t, oldScopedBaseline, currentScopedFindings, "prune-fixed", "scoped_rule"), "\n")
	if !strings.Contains(body, "existing scoped_rule finding") {
		t.Error("scoped prune-fixed should keep the existing scoped finding")
	}
	if !strings.Contains(body, "unrelated saved finding") {
		t.Error("scoped prune-fixed should preserve the out-of-scope row")
	}
	if strings.Contains(body, "new scoped_rule finding") {
		t.Error("scoped prune-fixed should not add the new scoped finding")
	}
	if strings.Contains(body, "fixed scoped_rule finding") {
		t.Error("scoped prune-fixed should drop the fixed scoped finding")
	}
}

// TestRewriteMatchesAwk is the byte-fidelity oracle: when awk is available, the
// Go rewriter must produce byte-identical body output to scripts/go-mk-baseline.awk
// for every mode and scope, so committed consumer baselines never churn.
func TestRewriteMatchesAwk(t *testing.T) {
	awkPath, err := exec.LookPath("awk")
	if err != nil {
		t.Skip("awk not available")
	}
	scriptPath, err := filepath.Abs("../../scripts/go-mk-baseline.awk")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(scriptPath); statErr != nil {
		t.Skipf("awk script not found: %v", statErr)
	}

	type fixture struct {
		name    string
		oldText string
		current string
		modes   []string
		scope   string
	}
	fixtures := []fixture{
		{"plain", oldBaseline, currentFindings, []string{"sync", "prune-fixed", "remove-fixed", "accept-new"}, ""},
		{"scoped", oldScopedBaseline, currentScopedFindings, []string{"sync", "prune-fixed", "accept-new"}, "scoped_rule"},
	}

	for _, fixtureCase := range fixtures {
		for _, mode := range fixtureCase.modes {
			name := fixtureCase.name + "/" + mode
			t.Run(name, func(t *testing.T) {
				directory := t.TempDir()
				oldPath := filepath.Join(directory, "old.baseline")
				currentPath := filepath.Join(directory, "current.findings")
				if writeErr := os.WriteFile(oldPath, []byte(fixtureCase.oldText+"\n"), 0o644); writeErr != nil {
					t.Fatal(writeErr)
				}
				if writeErr := os.WriteFile(currentPath, []byte(fixtureCase.current+"\n"), 0o644); writeErr != nil {
					t.Fatal(writeErr)
				}

				command := exec.Command(awkPath,
					"-v", "mode="+mode,
					"-v", "now=NOW",
					"-v", "label=sample",
					"-v", "current_file="+currentPath,
					"-v", "scope_pattern="+fixtureCase.scope,
					"-f", scriptPath,
					oldPath,
				)
				awkOutput, runErr := command.Output()
				if runErr != nil {
					t.Fatalf("awk run: %v", runErr)
				}

				body := bodyFor(t, fixtureCase.oldText, fixtureCase.current, mode, fixtureCase.scope)
				goOutput := ""
				for _, line := range body {
					goOutput += line + "\n"
				}
				if goOutput != string(awkOutput) {
					t.Errorf("Go output differs from awk for %s\n--- go ---\n%s\n--- awk ---\n%s",
						name, goOutput, string(awkOutput))
				}
			})
		}
	}
}
