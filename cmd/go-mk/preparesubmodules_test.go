package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSubmoduleInitPlanNestedGrammar(t *testing.T) {
	// Top-level .gitmodules declares third_party/gksyntax; gksyntax's own
	// .gitmodules declares the perl and swift grammar submodules plus a dart one.
	list := func(dir string) ([]string, error) {
		switch dir {
		case "":
			return []string{"third_party/gksyntax"}, nil
		case "third_party/gksyntax":
			return []string{
				"treesitter/grammars/perl/upstream",
				"treesitter/grammars/swift/upstream",
				"treesitter/grammars/dart/upstream",
			}, nil
		default:
			return nil, nil
		}
	}
	outputs := []string{
		"third_party/gksyntax/treesitter/grammars/swift/upstream/src/parser.c",
		"third_party/gksyntax/treesitter/grammars/perl/upstream/src/parser.c",
	}
	got, err := submoduleInitPlan(outputs, list)
	if err != nil {
		t.Fatalf("submoduleInitPlan returned error: %v", err)
	}
	want := []submoduleInit{
		{Parent: "", Path: "third_party/gksyntax"},
		{Parent: "third_party/gksyntax", Path: "treesitter/grammars/swift/upstream"},
		{Parent: "third_party/gksyntax", Path: "treesitter/grammars/perl/upstream"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("submoduleInitPlan() =\n  %v\nwant\n  %v", got, want)
	}
}

func TestSubmoduleInitPlanExcludesUnrelatedSubmodule(t *testing.T) {
	// The dart submodule holds no declared output, so it must never appear in
	// the plan. This keeps its git@ auth from ever running.
	list := func(dir string) ([]string, error) {
		if dir == "" {
			return []string{"third_party/gksyntax"}, nil
		}
		if dir == "third_party/gksyntax" {
			return []string{"treesitter/grammars/dart/upstream"}, nil
		}
		return nil, nil
	}
	outputs := []string{"third_party/gksyntax/other/file.go"}
	got, err := submoduleInitPlan(outputs, list)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []submoduleInit{{Parent: "", Path: "third_party/gksyntax"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v (no dart)", got, want)
	}
}

func TestSubmoduleInitPlanNoSubmodules(t *testing.T) {
	list := func(string) ([]string, error) { return nil, nil }
	got, err := submoduleInitPlan([]string{"internal/x.go"}, list)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("plan = %v, want empty", got)
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
