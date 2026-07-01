package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCgoDisabled(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  bool
	}{
		{name: "explicit zero disables", value: "0", set: true, want: true},
		{name: "explicit one enables", value: "1", set: true, want: false},
		{name: "unset keeps cgo default on", set: false, want: false},
		{name: "blank keeps cgo default on", value: "  ", set: true, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.set {
				t.Setenv("CGO_ENABLED", testCase.value)
			} else {
				t.Setenv("CGO_ENABLED", "")
			}
			if got := cgoDisabled(); got != testCase.want {
				t.Fatalf("cgoDisabled() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestCgoOptionalAllowlist(t *testing.T) {
	t.Setenv("GO_MK_CGO_OPTIONAL", "  example.com/a   example.com/b ")
	allow := cgoOptionalAllowlist()
	if !allow["example.com/a"] || !allow["example.com/b"] {
		t.Fatalf("allowlist missing declared packages: %v", allow)
	}
	if allow["example.com/c"] {
		t.Fatalf("allowlist contains undeclared package")
	}
}

func TestCgoStubTargetsPrefersCmd(t *testing.T) {
	t.Setenv("CMD", "./cmd/thing")
	if got := cgoStubTargets(); !reflect.DeepEqual(got, []string{"./cmd/thing"}) {
		t.Fatalf("cgoStubTargets() with CMD = %v", got)
	}
	t.Setenv("CMD", "")
	if got := cgoStubTargets(); !reflect.DeepEqual(got, []string{"./..."}) {
		t.Fatalf("cgoStubTargets() without CMD = %v", got)
	}
}

func TestFilterCgoRequiringPackages(t *testing.T) {
	packages := []cgoListPackage{
		{ImportPath: "net", Standard: true, CgoFiles: []string{"cgo_unix.go"}},
		{ImportPath: "os/user", Standard: true, CgoFiles: []string{"cgo_lookup.go"}},
		{ImportPath: "github.com/mattn/go-sqlite3", Standard: false, CgoFiles: []string{"sqlite3.go"}},
		{ImportPath: "example.com/optional", Standard: false, CgoFiles: []string{"x.go"}},
		{ImportPath: "example.com/purego", Standard: false, CgoFiles: nil},
	}
	allow := map[string]bool{"example.com/optional": true}
	got := filterCgoRequiringPackages(packages, allow)
	want := []string{"github.com/mattn/go-sqlite3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCgoRequiringPackages() = %v, want %v", got, want)
	}
}

func TestDecodeGoListPackages(t *testing.T) {
	// go list -json emits concatenated top-level objects, not an array.
	stream := `{"ImportPath":"net","Standard":true,"CgoFiles":["cgo_unix.go"]}
{"ImportPath":"github.com/mattn/go-sqlite3","Standard":false,"CgoFiles":["sqlite3.go"]}
{"ImportPath":"example.com/pure","Standard":false}`
	packages, err := decodeGoListPackages(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("decodeGoListPackages returned error: %v", err)
	}
	if len(packages) != 3 {
		t.Fatalf("decoded %d packages, want 3 (the More loop must not stop early)", len(packages))
	}
	got := filterCgoRequiringPackages(packages, nil)
	want := []string{"github.com/mattn/go-sqlite3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filter over decoded stream = %v, want %v", got, want)
	}
}

func TestDecodeGoListPackagesEmpty(t *testing.T) {
	packages, err := decodeGoListPackages(strings.NewReader(""))
	if err != nil {
		t.Fatalf("decodeGoListPackages(empty) returned error: %v", err)
	}
	if len(packages) != 0 {
		t.Fatalf("decoded %d packages from empty stream, want 0", len(packages))
	}
}

func TestCheckCgoStubNoopWhenCgoEnabled(t *testing.T) {
	t.Setenv("CGO_ENABLED", "1")
	if err := checkCgoStub(); err != nil {
		t.Fatalf("checkCgoStub() with cgo on should be a no-op, got %v", err)
	}
}

// TestCgoRequiringPackagesIgnoresStderrDownloadNoise guards the cold-cache fix:
// a stub `go` writes "go: downloading ..." progress to stderr while emitting the
// JSON package stream to stdout, mimicking a fresh release runner. Decoding must
// read stdout alone, so merging stdout and stderr back into one buffer would
// reintroduce the decode failure and fail this test.
func TestCgoRequiringPackagesIgnoresStderrDownloadNoise(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub go is a POSIX shell script")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "go")
	script := "#!/bin/sh\n" +
		"echo 'go: downloading github.com/mattn/go-sqlite3 v1.14.0' 1>&2\n" +
		"echo 'go: downloading example.com/pure v1.0.0' 1>&2\n" +
		`printf '%s\n' '{"ImportPath":"github.com/mattn/go-sqlite3","Standard":false,"CgoFiles":["sqlite3.go"]}'` + "\n" +
		`printf '%s\n' '{"ImportPath":"example.com/pure","Standard":false}'` + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub go: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := cgoRequiringPackages([]string{"./..."})
	if err != nil {
		t.Fatalf("cgoRequiringPackages returned error (stderr noise must not corrupt the JSON decode): %v", err)
	}
	want := []string{"github.com/mattn/go-sqlite3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cgoRequiringPackages() = %v, want %v", got, want)
	}
}
