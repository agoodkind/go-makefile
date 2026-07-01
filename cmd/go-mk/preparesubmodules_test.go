package main

import (
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
