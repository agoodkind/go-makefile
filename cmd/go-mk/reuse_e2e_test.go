package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// reuseHarness is a throwaway Go module driven through the real build entry
// point, so every assertion below is about what a consumer would observe from
// `make build`: whether the gate ran and whether a binary appeared.
type reuseHarness struct {
	t         *testing.T
	dir       string
	gateRuns  int
	gateCode  int
	reuseFlag string
}

// newReuseHarness writes a one-package module, points the environment at it the
// way go-build.mk would, and replaces the gate with a counter so a test can see
// whether a run did the expensive work or skipped it.
func newReuseHarness(t *testing.T, reuseFlag string) *reuseHarness {
	t.Helper()
	dir := t.TempDir()
	harness := &reuseHarness{t: t, dir: dir, reuseFlag: reuseFlag}

	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/tool\n\ngo 1.24\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() { println(greeting) }\n")
	writeFile(t, filepath.Join(dir, "greeting.go"), "package main\n\nconst greeting = \"one\"\n")

	chdir(t, dir)
	t.Setenv("GO_MK_REUSE_OUTPUTS", reuseFlag)
	t.Setenv("BINARY", "tool")
	t.Setenv("CMD", ".")
	t.Setenv("DIST_DIR", "dist")
	t.Setenv("INSTALL_DIR", filepath.Join(dir, "bin"))
	t.Setenv("INSTALL_BINS", "")
	t.Setenv("GO_BUILD_TAGS", "")
	t.Setenv("GO_BUILD_LDFLAGS", "")
	t.Setenv("GO_BUILD_EXTRA_FLAGS", "")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	if runtime.GOOS == "darwin" {
		// Ad-hoc signing needs no certificate, so the signing path still runs.
		t.Setenv("CODESIGN_IDENTITY", "-")
	}

	previousGate := runBuildGateFunc
	runBuildGateFunc = func() int {
		harness.gateRuns++
		return harness.gateCode
	}
	t.Cleanup(func() { runBuildGateFunc = previousGate })
	return harness
}

// build runs the real build entry point and fails the test on a non-zero exit.
func (harness *reuseHarness) build() {
	harness.t.Helper()
	if code := runBuild(); code != 0 {
		harness.t.Fatalf("runBuild = %d, want 0", code)
	}
}

// expectGateRuns asserts how many times the expensive path has run so far,
// which is the observable difference between a reused build and a real one.
func (harness *reuseHarness) expectGateRuns(want int, context string) {
	harness.t.Helper()
	if harness.gateRuns != want {
		harness.t.Fatalf("%s: gate ran %d times, want %d", context, harness.gateRuns, want)
	}
}

func (harness *reuseHarness) path(name string) string {
	return filepath.Join(harness.dir, name)
}

func TestBuildReusesWhenNothingChanged(t *testing.T) {
	harness := newReuseHarness(t, "1")

	harness.build()
	harness.expectGateRuns(1, "first build")
	if _, err := os.Stat(harness.path("dist/tool")); err != nil {
		t.Fatalf("first build produced no binary: %v", err)
	}

	harness.build()
	harness.expectGateRuns(1, "second build with an unchanged tree")
}

func TestBuildRebuildsWhenASourceChanges(t *testing.T) {
	harness := newReuseHarness(t, "1")
	harness.build()
	harness.build()
	harness.expectGateRuns(1, "unchanged tree")

	writeFile(t, harness.path("greeting.go"), "package main\n\nconst greeting = \"two\"\n")

	harness.build()
	harness.expectGateRuns(2, "after editing a source file")
}

func TestBuildRebuildsWhenASourceIsDeleted(t *testing.T) {
	harness := newReuseHarness(t, "1")
	writeFile(t, harness.path("extra.go"), "package main\n\nconst extra = 1\n\nvar _ = extra\n")
	harness.build()
	harness.build()
	harness.expectGateRuns(1, "unchanged tree")

	// A deleted file leaves every surviving file untouched, so a modification
	// time cannot see this. The file set is part of the fingerprint, so it can.
	if err := os.Remove(harness.path("extra.go")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	harness.build()
	harness.expectGateRuns(2, "after deleting a source file")
}

func TestBuildRebuildsWhenASourceIsAdded(t *testing.T) {
	harness := newReuseHarness(t, "1")
	harness.build()
	harness.build()
	harness.expectGateRuns(1, "unchanged tree")

	writeFile(t, harness.path("added.go"), "package main\n\nconst added = 1\n\nvar _ = added\n")

	harness.build()
	harness.expectGateRuns(2, "after adding a source file")
}

func TestBuildRebuildsWhenTheBinaryIsGone(t *testing.T) {
	harness := newReuseHarness(t, "1")
	harness.build()
	harness.build()
	harness.expectGateRuns(1, "unchanged tree")

	if err := os.Remove(harness.path("dist/tool")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	harness.build()
	harness.expectGateRuns(2, "after deleting the built binary")
}

func TestBuildRebuildsWhenTheBinaryWasReplaced(t *testing.T) {
	harness := newReuseHarness(t, "1")
	harness.build()
	harness.build()
	harness.expectGateRuns(1, "unchanged tree")

	// Another real binary, so the Go toolchain still agrees to overwrite the
	// output. What changed is that this is not the file the recorded run left.
	replaceWithOtherBinary(t, harness.path("dist/tool"))

	harness.build()
	harness.expectGateRuns(2, "after replacing the built binary")
}

// replaceWithOtherBinary overwrites path with the running test binary, which is
// a genuine executable the Go toolchain will overwrite on the next build.
func replaceWithOtherBinary(t *testing.T, path string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	body, readErr := os.ReadFile(self)
	if readErr != nil {
		t.Fatalf("ReadFile %s: %v", self, readErr)
	}
	if writeErr := os.WriteFile(path, body, 0o755); writeErr != nil {
		t.Fatalf("WriteFile %s: %v", path, writeErr)
	}
}

func TestBuildRebuildsWhenABuildSettingChanges(t *testing.T) {
	harness := newReuseHarness(t, "1")
	harness.build()
	harness.build()
	harness.expectGateRuns(1, "unchanged tree")

	t.Setenv("GO_BUILD_TAGS", "extra")

	harness.build()
	harness.expectGateRuns(2, "after changing the build tags")
}

// TestSourceListRefusesAPackageThatDidNotLoad covers the case a whole-tree test
// cannot reach: go list -e reports a package it could not load rather than
// failing, and that package's file list is incomplete. Fingerprinting an
// incomplete list would let a later change to a missing file go unseen, so the
// decoder refuses the whole list instead.
func TestSourceListRefusesAPackageThatDidNotLoad(t *testing.T) {
	stream := `{"Dir":"/repo/a","GoFiles":["a.go"]}
{"Dir":"/repo/b","Error":{"Err":"no required module provides package example.com/missing"}}`

	if _, err := decodeReuseSourceFiles([]byte(stream)); err == nil {
		t.Fatal("decodeReuseSourceFiles accepted a package that did not load")
	}

	healthy := `{"Dir":"/repo/a","GoFiles":["a.go"]}`
	files, err := decodeReuseSourceFiles([]byte(healthy))
	if err != nil {
		t.Fatalf("decodeReuseSourceFiles on a clean list: %v", err)
	}
	if len(files) != 1 || files[0] != "/repo/a/a.go" {
		t.Fatalf("decodeReuseSourceFiles = %v, want [/repo/a/a.go]", files)
	}
}

func TestBuildAlwaysRunsWhenReuseIsOff(t *testing.T) {
	harness := newReuseHarness(t, "")

	harness.build()
	harness.build()
	harness.build()
	harness.expectGateRuns(3, "three builds with reuse off")

	if _, err := os.Stat(harness.path(reuseStampFile)); err == nil {
		t.Fatal("reuse is off but the engine wrote a stamp")
	}
}

func TestBuildTimeStampDoesNotDefeatReuse(t *testing.T) {
	harness := newReuseHarness(t, "1")
	t.Setenv("VPKG", "example.com/tool")
	t.Setenv("GO_BUILD_LDFLAGS", "-X example.com/tool.BuildTime=2026-08-14T00:00:00Z")
	harness.build()
	harness.expectGateRuns(1, "first build")

	// The moment of the build is an output of the run, not an input to it, so a
	// later run whose only difference is that value must still reuse.
	t.Setenv("GO_BUILD_LDFLAGS", "-X example.com/tool.BuildTime=2026-08-14T09:30:00Z")

	harness.build()
	harness.expectGateRuns(1, "after only the build timestamp changed")
}

func TestNormalizeLdflagsKeepsEveryOtherStamp(t *testing.T) {
	got := normalizeLdflags("-X pkg.Commit=abc -X pkg.BuildTime=2026-08-14T00:00:00Z -X pkg.Version=v1")
	want := "-X pkg.Commit=abc -X pkg.Version=v1"
	if got != want {
		t.Fatalf("normalizeLdflags = %q, want %q", got, want)
	}
	if strings.Contains(got, "BuildTime") {
		t.Fatalf("normalizeLdflags left the build timestamp in %q", got)
	}
}
