package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestExtractFindingsDropsOutOfTree proves the cache-poisoning backstop: a
// finding whose file does not exist under the worktree root (the laundered
// sibling-worktree path) is dropped and counted, while a real local finding is
// kept and normalized.
func TestExtractFindingsDropsOutOfTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_MK_ROOT", root)

	rawPath := filepath.Join(root, "raw.out")
	rawLines := "cmd/main.go:6:11: real local finding (errcheck)\n" +
		"../../phantom/api/x.go:7:9: stale cache finding (godoclint)\n"
	if err := os.WriteFile(rawPath, []byte(rawLines), 0o644); err != nil {
		t.Fatal(err)
	}

	kept, dropped, err := extractFindings(rawPath, goFindingPattern.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if !slices.Contains(kept, "cmd/main.go:6:11: real local finding (errcheck)") {
		t.Errorf("local finding missing from kept: %v", kept)
	}
	for _, line := range kept {
		if strings.Contains(line, "phantom") {
			t.Errorf("out-of-tree finding survived: %q", line)
		}
	}
}

// envValue returns the value of key in a KEY=VALUE environment slice.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):], true
		}
	}
	return "", false
}

// TestLintEnvGolangciCacheDefault proves Component B: lintEnv defaults
// GOLANGCI_LINT_CACHE to a per-worktree path under .make so a worktree never
// shares golangci's content-addressed cache with a sibling.
func TestLintEnvGolangciCacheDefault(t *testing.T) {
	t.Setenv("GOLANGCI_LINT_CACHE", "")
	got, ok := envValue(lintEnv(), "GOLANGCI_LINT_CACHE")
	if !ok {
		t.Fatal("lintEnv did not set GOLANGCI_LINT_CACHE")
	}
	// golangci-lint requires an absolute cache path, so the default must be
	// absolute, not the relative ".make/golangci-cache".
	if !filepath.IsAbs(got) {
		t.Errorf("GOLANGCI_LINT_CACHE = %q, want an absolute path", got)
	}
	want, err := filepath.Abs(filepath.Join(makeDir, "golangci-cache"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("GOLANGCI_LINT_CACHE = %q, want %q", got, want)
	}
}

// TestLintEnvGolangciCacheRespectsOverride proves an explicit
// GOLANGCI_LINT_CACHE is never overridden.
func TestLintEnvGolangciCacheRespectsOverride(t *testing.T) {
	t.Setenv("GOLANGCI_LINT_CACHE", "/custom/golangci-cache")
	got, ok := envValue(lintEnv(), "GOLANGCI_LINT_CACHE")
	if !ok || got != "/custom/golangci-cache" {
		t.Errorf("GOLANGCI_LINT_CACHE = %q (ok=%v), want %q", got, ok, "/custom/golangci-cache")
	}
}

// TestToolFailedWithoutFindings proves that a non-zero golangci exit with no
// surfaced findings is a real tool failure only when nothing was dropped as
// out-of-tree; when out-of-tree findings were dropped, the non-zero exit is
// explained by them and the gate must not report a tool failure.
func TestToolFailedWithoutFindings(t *testing.T) {
	testCases := []struct {
		name    string
		status  int
		count   int
		dropped int
		want    bool
	}{
		{name: "genuine failure", status: 1, count: 0, dropped: 0, want: true},
		{name: "explained by drops", status: 1, count: 0, dropped: 2, want: false},
		{name: "clean run", status: 0, count: 0, dropped: 0, want: false},
		{name: "has findings", status: 1, count: 3, dropped: 0, want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := toolFailedWithoutFindings(testCase.status, testCase.count, testCase.dropped)
			if got != testCase.want {
				t.Errorf("toolFailedWithoutFindings(%d, %d, %d) = %v, want %v",
					testCase.status, testCase.count, testCase.dropped, got, testCase.want)
			}
		})
	}
}

// TestOutOfTreeNotice pins the user-facing line the golangci gate prints when it
// drops out-of-tree findings.
func TestOutOfTreeNotice(t *testing.T) {
	got := outOfTreeNotice(37)
	want := "ignored 37 finding(s) with out-of-tree paths (stale lint cache; run golangci-lint cache clean to clear)"
	if got != want {
		t.Errorf("outOfTreeNotice(37) = %q, want %q", got, want)
	}
}
