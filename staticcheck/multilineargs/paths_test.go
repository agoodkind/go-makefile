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

func TestAlignedNestedCallConvergesWithoutChangingOutput(t *testing.T) {
	const source = `package main
import ("fmt"; "strings")
type RequestEvent struct { DerivedCacheCreationTokens int; Stream bool }
func event(streamRequested bool, contentType string) RequestEvent {
return RequestEvent{
DerivedCacheCreationTokens: 0,
Stream: streamRequested || strings.Contains(contentType, "text/event-stream"),
}
}
func main() {
fmt.Println(event(false, "text/event-stream").Stream, event(true, "application/json").Stream, event(false, "").Stream)
}
`
	path := filepath.Join(t.TempDir(), "main.go")
	initial, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}

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
		t.Fatalf("formatting did not stabilize: %v", err)
	}

	command := exec.CommandContext(t.Context(), "go", "run", path)
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "true true false\n" {
		t.Fatalf("formatted program output = %q, error = %v", output, err)
	}
}

func TestBooleanLiteralCallsConvergeWithoutChangingOutput(t *testing.T) {
	const source = `package main
import "fmt"
type source struct {
Documents, CopyDocuments, ToolCalls []int
MissingNativeRecords int
}
type lookup struct { missingNativeRecords int; emptySource bool }
func build(source source) lookup {
return lookup{
missingNativeRecords: source.MissingNativeRecords,
emptySource: len(source.Documents) == 0 && len(source.CopyDocuments) == 0 && len(source.ToolCalls) == 0,
}
}
func main() {
empty := source{MissingNativeRecords: 7}
documents := source{Documents: []int{1}}
copies := source{CopyDocuments: []int{1}}
calls := source{ToolCalls: []int{1}}
fmt.Println(build(empty).emptySource, build(documents).emptySource, build(copies).emptySource, build(calls).emptySource, build(empty).missingNativeRecords)
}
`
	path := filepath.Join(t.TempDir(), "main.go")
	initial, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}

	before := runBooleanProgram(t, path)
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
		t.Fatalf("second formatting pass changed source: %v", err)
	}

	if !strings.Contains(string(first), "len(source.Documents)") ||
		!strings.Contains(string(first), "len(source.CopyDocuments)") {
		t.Fatalf("short calls did not remain compact:\n%s", first)
	}

	for _, line := range strings.Split(string(first), "\n") {
		if width := len(strings.ReplaceAll(line, "\t", "    ")); width > 100 {
			t.Fatalf("formatted line has %d columns: %s", width, line)
		}
	}

	if after := runBooleanProgram(t, path); after != before ||
		after != "true false false false 7\n" {
		t.Fatalf("program output changed from %q to %q", before, after)
	}
}

func runBooleanProgram(t *testing.T, path string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", "run", path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run boolean program: %v\n%s", err, output)
	}

	return string(output)
}
