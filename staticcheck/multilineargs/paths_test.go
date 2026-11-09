package multilineargs_test

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
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
