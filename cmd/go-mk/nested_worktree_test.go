package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// withWorkdir cds into dir for the duration of the test and restores the
// previous working directory on cleanup. nestedWorktreeRoots walks from
// ".", so tests must drive it from a temp tree they control.
func withWorkdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir(%q): %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore os.Chdir(%q): %v", prev, err)
		}
	})
}

// writeFile creates path with content, creating parent directories as
// needed. Tests use it to drop placeholder .git files and stub .go files
// into a temp tree.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func TestNestedWorktreeRootsEmptyTree(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	roots, err := nestedWorktreeRoots()
	if err != nil {
		t.Fatalf("nestedWorktreeRoots: %v", err)
	}
	if len(roots) != 0 {
		t.Fatalf("expected empty set, got %v", roots)
	}
}

func TestNestedWorktreeRootsOwnGitDirNotReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	withWorkdir(t, dir)
	roots, err := nestedWorktreeRoots()
	if err != nil {
		t.Fatalf("nestedWorktreeRoots: %v", err)
	}
	if len(roots) != 0 {
		t.Fatalf("expected own .git to be ignored, got %v", roots)
	}
}

func TestNestedWorktreeRootsFindsWorktreeFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", ".git"), "gitdir: /elsewhere\n")
	writeFile(t, filepath.Join(dir, "sub", "main.go"), "package x\n")
	withWorkdir(t, dir)
	roots, err := nestedWorktreeRoots()
	if err != nil {
		t.Fatalf("nestedWorktreeRoots: %v", err)
	}
	if _, ok := roots["./sub"]; !ok {
		t.Fatalf("expected ./sub in roots, got %v", roots)
	}
}

func TestNestedWorktreeRootsFindsNestedClone(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vendored", ".git", "objects"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(dir, "vendored", "thing.go"), "package thing\n")
	withWorkdir(t, dir)
	roots, err := nestedWorktreeRoots()
	if err != nil {
		t.Fatalf("nestedWorktreeRoots: %v", err)
	}
	if _, ok := roots["./vendored"]; !ok {
		t.Fatalf("expected ./vendored in roots, got %v", roots)
	}
}

func TestNestedWorktreeRootsDeeplyNestedReportedOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a", ".git"), "gitdir: /a\n")
	writeFile(t, filepath.Join(dir, "a", "b", ".git"), "gitdir: /b\n")
	writeFile(t, filepath.Join(dir, "a", "b", "main.go"), "package b\n")
	withWorkdir(t, dir)
	roots, err := nestedWorktreeRoots()
	if err != nil {
		t.Fatalf("nestedWorktreeRoots: %v", err)
	}
	if _, ok := roots["./a"]; !ok {
		t.Fatalf("expected ./a in roots, got %v", roots)
	}
	if _, ok := roots["./a/b"]; ok {
		t.Fatalf("did not expect ./a/b to be reported once ./a is recorded, got %v", roots)
	}
}

func TestNestedWorktreeRootsEscapeHatchDisablesDiscovery(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", ".git"), "gitdir: /elsewhere\n")
	withWorkdir(t, dir)
	t.Setenv("GO_MK_LINT_INCLUDE_NESTED_WORKTREES", "1")
	roots, err := nestedWorktreeRoots()
	if err != nil {
		t.Fatalf("nestedWorktreeRoots: %v", err)
	}
	if len(roots) != 0 {
		t.Fatalf("escape hatch must disable discovery, got %v", roots)
	}
}

func TestPathInAnyRootMatchesDirectAndDescendant(t *testing.T) {
	t.Parallel()
	roots := map[string]struct{}{
		"./a/b": {},
	}
	cases := []struct {
		path string
		want bool
	}{
		{"./a/b", true},
		{"./a/b/foo.go", true},
		{"./a/b/sub/deep.go", true},
		{"a/b", true},
		{"a/b/foo.go", true},
		{"./a", false},
		{"./a/c", false},
		{"./other/a/b", false},
	}
	for _, tc := range cases {
		got := pathInAnyRoot(tc.path, roots)
		if got != tc.want {
			t.Errorf("pathInAnyRoot(%q): got %v want %v", tc.path, got, tc.want)
		}
	}
}

func TestDropPathsUnderNestedWorktreesPassThrough(t *testing.T) {
	t.Parallel()
	paths := []string{"./a.go", "./b.go"}
	got := dropPathsUnderNestedWorktrees(paths, map[string]struct{}{})
	if !slices.Equal(got, paths) {
		t.Fatalf("empty roots must be a pass-through; got %v", got)
	}
}

func TestDropPathsUnderNestedWorktreesFiltersDescendants(t *testing.T) {
	t.Parallel()
	roots := map[string]struct{}{"./.claude/worktrees/wt": {}}
	paths := []string{
		"./.claude/worktrees/wt/api/foo.go",
		"./.claude/worktrees/wt",
		"./internal/foo.go",
		"./.claude/agents/keep.go",
	}
	got := dropPathsUnderNestedWorktrees(paths, roots)
	want := []string{"./internal/foo.go", "./.claude/agents/keep.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestIsTruthy(t *testing.T) {
	t.Parallel()
	for _, on := range []string{"1", "true", "TRUE", "Yes", " on "} {
		if !isTruthy(on) {
			t.Errorf("isTruthy(%q) = false, want true", on)
		}
	}
	for _, off := range []string{"", "0", "false", "no", "off", "maybe"} {
		if isTruthy(off) {
			t.Errorf("isTruthy(%q) = true, want false", off)
		}
	}
}
