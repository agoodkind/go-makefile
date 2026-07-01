package main

import (
	"reflect"
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
