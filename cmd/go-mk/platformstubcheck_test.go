package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestRecordPlatformPresence(t *testing.T) {
	presence := map[string]*platformCgoPresence{}
	// darwin/arm64: the real cgo engine plus a pure-Go package.
	recordPlatformPresence(presence, []platformListPackage{
		{ImportPath: "mod/cbm", CgoFiles: []string{"engine.go"}},
		{ImportPath: "mod/util", GoFiles: []string{"util.go"}},
		{ImportPath: "runtime/cgo", Standard: true, CgoFiles: []string{"cgo.go"}},
	})
	// linux/amd64: the cbm package now resolves to its pure-Go stub sibling.
	recordPlatformPresence(presence, []platformListPackage{
		{ImportPath: "mod/cbm", GoFiles: []string{"engine_stub.go"}},
		{ImportPath: "mod/util", GoFiles: []string{"util.go"}},
	})
	if !presence["mod/cbm"].withCgo || !presence["mod/cbm"].withoutCgo {
		t.Fatalf("cbm should be recorded present with and without cgo, got %+v", presence["mod/cbm"])
	}
	if presence["mod/util"].withCgo {
		t.Fatalf("pure-Go util must never be recorded withCgo")
	}
	if _, ok := presence["runtime/cgo"]; ok {
		t.Fatalf("standard-library packages must be skipped")
	}
}

func TestRecordPlatformPresenceSkipsAbsentPackage(t *testing.T) {
	presence := map[string]*platformCgoPresence{}
	// A package with no matched files is absent on this platform and must not
	// register as present-without-cgo (which would be a false split).
	recordPlatformPresence(presence, []platformListPackage{
		{ImportPath: "mod/darwinonly"},
	})
	if _, ok := presence["mod/darwinonly"]; ok {
		t.Fatalf("a package with no files must not be recorded")
	}
}

func TestFlagPlatformSplitPackages(t *testing.T) {
	presence := map[string]*platformCgoPresence{
		"mod/cbm":     {withCgo: true, withoutCgo: true},  // the stub pattern
		"mod/allcgo":  {withCgo: true, withoutCgo: false}, // cgo on every platform, fine
		"mod/purego":  {withCgo: false, withoutCgo: true}, // never cgo, fine
		"mod/deliber": {withCgo: true, withoutCgo: true},  // stub pattern but allowlisted
	}
	got := flagPlatformSplitPackages(presence, map[string]bool{"mod/deliber": true})
	want := []string{"mod/cbm"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flagPlatformSplitPackages() = %v, want %v", got, want)
	}
}

func TestPlatformListEnvOverridesStalePlatform(t *testing.T) {
	// A parent environment that already pins GOOS/GOARCH/CGO_ENABLED must be
	// overridden, not duplicated, so every platform's go list resolves for its
	// own target and a real split is not hidden.
	base := []string{"PATH=/usr/bin", "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0"}
	env := platformListEnv(base, "darwin", "arm64")
	want := map[string]string{"GOOS": "darwin", "GOARCH": "arm64", "CGO_ENABLED": "1"}
	for key, value := range want {
		prefix := key + "="
		count := 0
		var got string
		for _, entry := range env {
			if strings.HasPrefix(entry, prefix) {
				count++
				got = entry[len(prefix):]
			}
		}
		if count != 1 {
			t.Fatalf("%s appears %d times, want exactly 1 (no duplicates): %v", key, count, env)
		}
		if got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}
}

func TestPlatformStubAllowlist(t *testing.T) {
	t.Setenv("GO_MK_PLATFORM_STUB_OPTIONAL", "  mod/a   mod/b ")
	allow := platformStubAllowlist()
	if !allow["mod/a"] || !allow["mod/b"] {
		t.Fatalf("allowlist missing declared packages: %v", allow)
	}
	if allow["mod/c"] {
		t.Fatalf("allowlist contains undeclared package")
	}
}
