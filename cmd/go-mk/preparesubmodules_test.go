package main

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"
)

// Nested declarations become visible after initialization in this filesystem.
type revealingFilesystem struct {
	layout      map[string][]string
	initialized map[string]bool
	initCalls   []string
}

func newRevealingFilesystem(layout map[string][]string) *revealingFilesystem {
	return &revealingFilesystem{
		layout:      layout,
		initialized: map[string]bool{"": true},
	}
}

func (fs *revealingFilesystem) list(dir string) ([]string, error) {
	if !fs.initialized[dir] {
		return nil, nil // .gitmodules is not on disk until dir is checked out
	}
	return fs.layout[dir], nil
}

func (fs *revealingFilesystem) init(parent, submodulePath string) error {
	full := path.Join(parent, submodulePath)
	fs.initialized[full] = true
	fs.initCalls = append(fs.initCalls, full)
	return nil
}

func TestPrepareSubmodulesForOutputsNoSubmodules(t *testing.T) {
	fs := newRevealingFilesystem(map[string][]string{})
	if err := prepareSubmodulesForOutputs([]string{"internal/x.go"}, fs.list, fs.init); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fs.initCalls) != 0 {
		t.Fatalf("init calls = %v, want none", fs.initCalls)
	}
}

func TestGitListSubmodulesParsesPaths(t *testing.T) {
	dir := t.TempDir()
	content := "" +
		"[submodule \"a\"]\n\tpath = third_party/gksyntax\n\turl = https://github.com/x/gksyntax\n" +
		"[submodule \"b\"]\n\tpath = vendor/other\n\turl = https://github.com/x/other\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := gitListSubmodules(dir)
	if err != nil {
		t.Fatalf("gitListSubmodules error: %v", err)
	}
	want := []string{"third_party/gksyntax", "vendor/other"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gitListSubmodules() = %v, want %v", got, want)
	}
}

func TestGitListSubmodulesMissingFileIsEmpty(t *testing.T) {
	got, err := gitListSubmodules(t.TempDir())
	if err != nil {
		t.Fatalf("expected no error for missing .gitmodules, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
