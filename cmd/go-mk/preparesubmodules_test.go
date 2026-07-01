package main

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"
)

// revealingFilesystem simulates a checkout where a nested .gitmodules is only
// readable after its parent submodule has been initialized. layout maps a
// directory to the submodules its .gitmodules declares; a directory's entries
// are visible only once it has been initialized (the repo root, "", is always
// visible). This is the real workflow condition the interleaved walk must handle.
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

func TestPrepareSubmodulesForOutputsInterleavesNestedDiscovery(t *testing.T) {
	// The nested grammar submodules are only discoverable after third_party/gksyntax
	// is initialized. An upfront plan (computed before any init) would find only the
	// top-level submodule; the interleaved walk must reach the nested grammars.
	fs := newRevealingFilesystem(map[string][]string{
		"": {"third_party/gksyntax"},
		"third_party/gksyntax": {
			"treesitter/grammars/perl/upstream",
			"treesitter/grammars/swift/upstream",
			"treesitter/grammars/dart/upstream",
		},
	})
	outputs := []string{
		"third_party/gksyntax/treesitter/grammars/swift/upstream/src/parser.c",
		"third_party/gksyntax/treesitter/grammars/perl/upstream/src/parser.c",
	}
	if err := prepareSubmodulesForOutputs(outputs, fs.list, fs.init); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"third_party/gksyntax",
		"third_party/gksyntax/treesitter/grammars/swift/upstream",
		"third_party/gksyntax/treesitter/grammars/perl/upstream",
	}
	if !reflect.DeepEqual(fs.initCalls, want) {
		t.Fatalf("init calls =\n  %v\nwant\n  %v", fs.initCalls, want)
	}
}

func TestPrepareSubmodulesForOutputsExcludesUnrelatedSubmodule(t *testing.T) {
	// The dart submodule holds no declared output, so it must never be initialized.
	// This keeps its git@ auth from ever running on CI.
	fs := newRevealingFilesystem(map[string][]string{
		"":                     {"third_party/gksyntax"},
		"third_party/gksyntax": {"treesitter/grammars/dart/upstream"},
	})
	outputs := []string{"third_party/gksyntax/other/file.go"}
	if err := prepareSubmodulesForOutputs(outputs, fs.list, fs.init); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"third_party/gksyntax"}
	if !reflect.DeepEqual(fs.initCalls, want) {
		t.Fatalf("init calls = %v, want %v (no dart)", fs.initCalls, want)
	}
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
