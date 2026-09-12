package main

import (
	"errors"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStaticcheckExtraGo127GenericMethods(t *testing.T) {
	t.Parallel()
	// This fixture verifies Go 1.27 syntax when that compiler runs the suite.
	if !slices.Contains(build.Default.ReleaseTags, "go1.27") {
		t.Skip("generic methods require Go 1.27")
	}

	binary := filepath.Join(t.TempDir(), "staticcheck-extra")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build staticcheck-extra: %v\n%s", err, output)
	}

	fixture := t.TempDir()
	writeFixtureFile(t, fixture, "go.mod", "module example.com/genericmethod\n\ngo 1.27.1\n")
	writeFixtureFile(t, fixture, "value/value.go", `package value

type Number int

func (number Number) Add[T ~int](other T) T {
	return T(number) + other
}
`)
	writeFixtureFile(t, fixture, "consumer.go", `package consumer

import "example.com/genericmethod/value"

func Sum() int {
	return value.Number(2).Add(3)
}
`)
	compile := exec.Command("go", "test", "./...")
	compile.Dir = fixture
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile generic-method fixture: %v\n%s", err, output)
	}

	analyze := exec.Command(binary, "-no_any_or_empty_interface", ".")
	analyze.Dir = fixture
	if output, err := analyze.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatalf("analyze generic-method fixture: %v\n%s", err, output)
	}

	writeFixtureFile(t, fixture, "loose.go", `package consumer

func Loose(input any) {}
`)
	analyze = exec.Command(binary, "-no_any_or_empty_interface", ".")
	analyze.Dir = fixture
	output, err := analyze.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 3 {
		t.Fatalf("analyzer exit = %v, want diagnostic exit 3\n%s", err, output)
	}
	if !strings.Contains(string(output), "loose.go:3:18: do not use any") {
		t.Fatalf("analyzer did not report the loose parameter:\n%s", output)
	}
}

func writeFixtureFile(t *testing.T, directory, name, content string) {
	t.Helper()

	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
