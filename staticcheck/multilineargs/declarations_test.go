package multilineargs_test

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"goodkind.io/go-makefile/staticcheck/multilineargs"
)

func TestGroupedDeclarationFormattingConvergesWithoutChangingOutput(t *testing.T) {
	const source = `package main
import ("errors"; "fmt")
var (
errRepositoryCacheFull = errors.New("repository Git object cache exceeds its disk byte limit")
errRepositoryShallowUnsupported = errors.New("repository Git server must support bounded shallow acquisition")
)
func main() { fmt.Println(errRepositoryCacheFull); fmt.Println(errRepositoryShallowUnsupported) }
`
	initial, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := multilineargs.FormatPaths([]string{path}); err != nil {
		t.Fatal(err)
	}

	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := multilineargs.FormatPaths([]string{path}); err != nil {
		t.Fatal(err)
	}

	second, err := os.ReadFile(path)
	if err != nil || string(first) != string(second) {
		t.Fatalf("declaration formatting did not stabilize: %v", err)
	}

	command := exec.CommandContext(t.Context(), "go", "run", path)
	output, err := command.CombinedOutput()
	const expected = "repository Git object cache exceeds its disk byte limit\nrepository Git server must support bounded shallow acquisition\n"
	if err != nil || string(output) != expected {
		t.Fatalf("formatted program output = %q, error = %v", output, err)
	}
}

func TestDeclarationCommentsKeepShortCallsCompact(t *testing.T) {
	const source = `package widget
import "errors"
var (
first = /* retain the first initializer comment */ errors.New("short")
second = errors.New("a call that fits beside the existing declaration prefix")
)
`
	initial, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "widget.go")
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := multilineargs.FormatPaths([]string{path}); err != nil {
		t.Fatal(err)
	}

	formatted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(
		string(formatted),
		`errors.New("a call that fits beside the existing declaration prefix")`,
	) {
		t.Fatalf("an initializer comment expanded another call:\n%s", formatted)
	}
}
