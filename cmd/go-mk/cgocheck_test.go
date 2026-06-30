package main

import (
	"reflect"
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

func TestCheckCgoStubNoopWhenCgoEnabled(t *testing.T) {
	t.Setenv("CGO_ENABLED", "1")
	if err := checkCgoStub(); err != nil {
		t.Fatalf("checkCgoStub() with cgo on should be a no-op, got %v", err)
	}
}
