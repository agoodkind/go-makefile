package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// makeImmutable makes path un-removable and returns a function that undoes it.
//
// The mechanism differs by platform and both are covered, because the earlier
// version called chflags unconditionally and skipped when it was missing. CI
// runs on ubuntu-24.04, where chflags does not exist, so that test never ran
// where it mattered and an unchecked-removal regression would have reached a
// green branch. It skips only when neither mechanism is available, and says
// which one it used.
func makeImmutable(t *testing.T, path string) func() {
	t.Helper()
	// BSD and macOS.
	if _, err := exec.LookPath("chflags"); err == nil {
		if output, err := exec.Command("chflags", "uchg", path).CombinedOutput(); err == nil {
			return func() { _ = exec.Command("chflags", "nouchg", path).Run() }
		} else {
			t.Logf("chflags present but failed, trying chattr: %v: %s", err, output)
		}
	}
	// Linux. Needs CAP_LINUX_IMMUTABLE, which a container job usually has and
	// an unprivileged local user usually does not.
	if _, err := exec.LookPath("chattr"); err == nil {
		if output, err := exec.Command("chattr", "+i", path).CombinedOutput(); err == nil {
			return func() { _ = exec.Command("chattr", "-i", path).Run() }
		} else {
			t.Logf("chattr present but failed: %v: %s", err, output)
		}
	}
	t.Skip("no working immutable-flag mechanism here (tried chflags and chattr)")
	return func() {}
}

// inodeOf returns the inode number of path. install_self's whole purpose is to
// replace the running script by renaming a new file over it rather than writing
// through the existing one, and the inode is the only deterministic, non-timing
// observation of which of those two happened: a copy keeps the inode, a rename
// replaces it.
func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat %s: no syscall.Stat_t, cannot read the inode", path)
	}
	return uint64(stat.Ino)
}

// helperFiles is the engine tree the test server serves. It carries every asset
// the helper must provision, so a cold run has a complete source.
func helperFiles() map[string]string {
	return map[string]string{
		"go.mk":                      "# go.mk v1\n",
		"golangci.yml":               "version: \"2\"\n",
		"notices.txt":                "notice v1\n",
		"scripts/go-mk-fetch-one.sh": "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-bin.sh":       "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-sync.sh":      "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-bootstrap.sh": "#!/usr/bin/env bash\nexit 0\n",
	}
}

func TestHelperColdProvisionWritesEveryAsset(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	stdout, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	for _, asset := range []string{"go.mk", "golangci.yml", "notices.txt", "scripts/go-mk-fetch-one.sh"} {
		if body := readAsset(t, dir, asset); body == "" {
			t.Fatalf("asset %s missing or empty after cold provision", asset)
		}
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the served body", got)
	}
	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("server saw %d requests, want exactly 1", len(requests))
	}
	// Assert the URL, not just that a request happened. This server answers
	// every path with the same fixture, so a helper pointed at the wrong
	// repository or the wrong ref would still get a normal 200 and every other
	// assertion here would still pass.
	const wantPath = "/agoodkind/go-makefile/tar.gz/main"
	if requests[0].Path != wantPath {
		t.Fatalf("cold provision requested %q, want %q", requests[0].Path, wantPath)
	}
}

// TestHelperLeavesAssetsIntactWhenUpstreamIsUnreachable exercises the
// non-destructive contract against an unreachable upstream. It requires exit
// code 1 (the helper's own failure exit from main, set by the script after it
// reaches its provisioning decision) and requires the helper's own failure
// message on stderr. A missing scripts/go-mk-bootstrap.sh would make bash
// itself exit 127 with a "No such file or directory" message rather than
// running any script logic at all, which would satisfy a bare "code == 0"
// check without the helper ever having run. Pinning the exact code and the
// helper's own wording rules that out.
func TestHelperLeavesAssetsIntactWhenUpstreamIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	writeAsset(t, dir, "go.mk", "# warm go.mk\n")
	writeAsset(t, dir, "golangci.yml", "version: \"2\"\n")
	writeAsset(t, dir, "notices.txt", "warm notice\n")
	writeAsset(t, dir, "scripts/go-mk-fetch-one.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-bin.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-sync.sh", "#!/usr/bin/env bash\nexit 0\n")

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": unreachableCodeloadBase(t),
	})
	if code != 1 {
		t.Fatalf("helper exit = %d, want 1 (the script's own failure exit, not e.g. 127 from a missing script)\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "could not provision go-makefile assets") {
		t.Fatalf("stderr = %q, want it to contain the helper's own provisioning-failure message, proving the script actually ran and reached its decision", stderr)
	}

	// The point of the task: a failed fetch must not remove what was there.
	if got := readAsset(t, dir, "go.mk"); got != "# warm go.mk\n" {
		t.Fatalf("go.mk = %q after a failed fetch, want the original body preserved", got)
	}
	if got := readAsset(t, dir, "golangci.yml"); got == "" {
		t.Fatal("golangci.yml was destroyed by a failed fetch")
	}
}

// TestHelperLeavesAssetsIntactWhenTarballIsIncomplete covers a 200 response
// whose tarball is missing required assets: the previous behavior was already
// correct here, but nothing exercised it. It also asserts the surviving
// assets are byte-for-byte identical to what was there before the run.
func TestHelperLeavesAssetsIntactWhenTarballIsIncomplete(t *testing.T) {
	files := helperFiles()
	delete(files, "notices.txt")
	delete(files, "scripts/go-mk-sync.sh")
	server := newFetchServer(t, files)

	dir := t.TempDir()
	writeAsset(t, dir, "go.mk", "# warm go.mk\n")
	writeAsset(t, dir, "golangci.yml", "version: \"2\"\n")
	writeAsset(t, dir, "notices.txt", "warm notice\n")
	writeAsset(t, dir, "scripts/go-mk-fetch-one.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-bin.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-sync.sh", "#!/usr/bin/env bash\nexit 0\n")

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code == 0 {
		t.Fatalf("helper exit = 0, want non-zero when the served tarball is missing required assets\nstderr: %s", stderr)
	}

	for relative, want := range map[string]string{
		"go.mk":                      "# warm go.mk\n",
		"golangci.yml":               "version: \"2\"\n",
		"notices.txt":                "warm notice\n",
		"scripts/go-mk-fetch-one.sh": "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-bin.sh":       "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-sync.sh":      "#!/usr/bin/env bash\nexit 0\n",
	} {
		if got := readAsset(t, dir, relative); got != want {
			t.Fatalf("%s = %q after an incomplete tarball, want the original body %q preserved byte for byte", relative, got, want)
		}
	}
}

// TestHelperMidInstallFailureExitsNonZero forces install_one_asset to fail on
// a required asset that sits neither first nor last in required_assets()'s
// order (golangci.yml, the second of six), by pre-creating a DIRECTORY at its
// path, which the install refuses to replace. The assets before it (go.mk)
// and after it (notices.txt, every scripts/* asset) install successfully.
// The subshell running provision's staging work executes with errexit
// suppressed (it inherits that from being called as the condition of an if),
// so install_from_stage's implicit return value was just whatever its last
// loop iteration happened to return; since that last iteration
// (scripts/go-mk-sync.sh) still succeeds here, a naive test that breaks the
// last asset instead of a middle one would pass even with the bug present.
// Breaking a middle asset is what actually distinguishes "every step's
// failure propagates" from "only the last step's failure happens to be
// visible".
//
// A directory is the injection rather than a read-only file because the
// install writes a sibling temporary and renames it into place, and rename
// replaces a read-only file without error; a directory is the shape it must
// refuse. A directory also blocks root, so this needs no root skip.
func TestHelperMidInstallFailureExitsNonZero(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	makeDir := filepath.Join(dir, ".make")
	if err := os.MkdirAll(makeDir, 0o755); err != nil {
		t.Fatalf("mkdir .make: %v", err)
	}
	golangciPath := filepath.Join(makeDir, "golangci.yml")
	if err := os.MkdirAll(golangciPath, 0o755); err != nil {
		t.Fatalf("seed directory at golangci.yml: %v", err)
	}

	// Seed state from a previous, successful run, so the failure below has
	// something stale to leave behind.
	writeState(t, dir, "main", `"old-etag"`, time.Now().Add(-10*time.Minute).Unix())

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code == 0 {
		t.Fatalf("helper exit = 0, want non-zero when a middle install step (golangci.yml) fails while the surrounding steps succeed\nstderr: %s", stderr)
	}
	// A failure here leaves .make holding a mix of new and old assets, because
	// install_from_stage replaces them one at a time. The stale state file must
	// not survive that: if it did, the next run would send its ETag, get a 304,
	// and treat the mixed tree as validated and current forever.
	if _, err := os.Stat(filepath.Join(dir, ".make", ".go-mk-fetch-state")); !os.IsNotExist(err) {
		t.Fatalf("state file still present after a partial install (stat err = %v); "+
			"a later run would 304 against it and bless a tree that is half new and half old", err)
	}
}

// TestHelperDevDirReplacesItsOwnFileAndClearsState covers the dev-dir install
// path, which had two defects the staged path did not. It installed this
// script with a plain copy, writing through the inode bash was reading, and it
// left the recorded ETag in place even though .make now holds local edits that
// never came from upstream.
func TestHelperDevDirReplacesItsOwnFileAndClearsState(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	if _, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	}); code != 0 {
		t.Fatalf("cold provision exit = %d, want 0: %s", code, stderr)
	}
	statePath := filepath.Join(dir, ".make", ".go-mk-fetch-state")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("cold provision left no state file: %v", err)
	}
	helperPath := filepath.Join(dir, ".make", "scripts", "go-mk-bootstrap.sh")
	inodeBefore := inodeOf(t, helperPath)

	devDir := t.TempDir()
	for name, body := range helperFiles() {
		if name == "go.mk" {
			body = "# go.mk from the dev dir\n"
		}
		if name == "scripts/go-mk-bootstrap.sh" {
			body = "#!/usr/bin/env bash\n# dev dir helper\nexit 0\n"
		}
		path := filepath.Join(devDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if _, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_DEV_DIR":       devDir,
		"GO_MK_CODELOAD_BASE": unreachableCodeloadBase(t),
	}); code != 0 {
		t.Fatalf("dev-dir run exit = %d, want 0: %s", code, stderr)
	}

	if got := readAsset(t, dir, "go.mk"); got != "# go.mk from the dev dir\n" {
		t.Fatalf("go.mk = %q, want the dev dir body", got)
	}
	if inodeAfter := inodeOf(t, helperPath); inodeAfter == inodeBefore {
		t.Fatalf("helper inode unchanged (%d) after a dev-dir install that replaced its body; "+
			"the running script's file was written through rather than replaced", inodeBefore)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file survived a dev-dir install (stat err = %v); "+
			"a later run without GO_MK_DEV_DIR would send that ETag, get a 304, and treat the "+
			"developer's local edits as current upstream content", err)
	}
}

// TestHelperDevDirPartialInstallLeavesNoState covers the ordering of the state
// removal on the dev-dir path. The install loop stops on its first failure, so
// a failure partway leaves .make holding some dev-dir files and some upstream
// ones. If the recorded state survived that, the next run without
// GO_MK_DEV_DIR would send its ETag, receive 304, and treat the half-replaced
// tree as current upstream content indefinitely.
func TestHelperDevDirPartialInstallLeavesNoState(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	if _, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	}); code != 0 {
		t.Fatalf("cold provision exit = %d, want 0: %s", code, stderr)
	}
	statePath := filepath.Join(dir, ".make", ".go-mk-fetch-state")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("cold provision left no state: %v", err)
	}

	// A dev dir carrying every asset, with a directory seeded at one target so
	// the install fails after earlier assets have already been replaced. A
	// read-only file would no longer do it: the install renames a sibling
	// temporary into place, and rename replaces a read-only file without error.
	devDir := t.TempDir()
	for name, body := range helperFiles() {
		path := filepath.Join(devDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	blockedPath := filepath.Join(dir, ".make", "golangci.yml")
	if err := os.Remove(blockedPath); err != nil {
		t.Fatalf("remove installed golangci.yml: %v", err)
	}
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("seed directory at golangci.yml: %v", err)
	}

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_DEV_DIR":       devDir,
		"GO_MK_CODELOAD_BASE": unreachableCodeloadBase(t),
	})
	if code == 0 {
		t.Fatalf("dev-dir run exit = 0, want non-zero when an install step fails: %s", stderr)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file survived a partial dev-dir install (stat err = %v); "+
			"a later run would 304 against it and bless a tree that is part dev dir and part upstream", err)
	}
}

// On the degrade path for a failed state write, which has no test here:
// provision() warns and exits 0 rather than failing when write_state fails,
// because .make is installed and re-verified by that point and aborting would
// trade a complete engine for none.
//
// That branch used to have a test, which seeded a directory at the state path
// so the write would fail. clear_state now runs BEFORE any asset is installed
// and rejects that same input first, so the helper refuses early instead of
// reaching the write. Refusing is the better outcome and the test below
// asserts it. Once the state is known removed, a later write to that path
// fails only from something like a full disk between the install and the
// write, which this suite cannot produce deterministically. The branch is
// therefore defense in depth with no coverage claimed for it, rather than a
// test rewritten until it passed.
//
// TestHelperRefusesToInstallWhenStateCannotBeCleared covers the check on that
// removal. Removing the state before installing only helps if the removal
// actually happened: an unchecked failure would leave assets changing
// underneath a state file that still describes the old ones, which is the
// precise condition the ordering exists to prevent.
func TestHelperRefusesToInstallWhenStateCannotBeCleared(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root, where an unwritable directory cannot block removal")
	}
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	if _, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	}); code != 0 {
		t.Fatalf("cold provision exit = %d, want 0: %s", code, stderr)
	}

	// Make ONLY the state file un-removable, with the immutable flag rather
	// than by denying write on .make. Denying write on the directory would
	// also block every asset install, so the helper would fail for that
	// reason instead and this test would pass without exercising the check at
	// all. Confirmed: with .make at 0555 the test passed even against an
	// unchecked removal.
	statePathForFlag := filepath.Join(dir, ".make", ".go-mk-fetch-state")
	goMkBefore := readAsset(t, dir, "go.mk")
	clearFlag := makeImmutable(t, statePathForFlag)
	t.Cleanup(clearFlag)

	server.SetFiles(map[string]string{
		"go.mk":                      "# go.mk v2\n",
		"golangci.yml":               "version: \"2\"\n",
		"notices.txt":                "notice v1\n",
		"scripts/go-mk-fetch-one.sh": "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-bin.sh":       "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-sync.sh":      "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-bootstrap.sh": "#!/usr/bin/env bash\nexit 0\n",
	})

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code == 0 {
		t.Fatalf("helper exit = 0, want non-zero when the state file cannot be removed: %s", stderr)
	}
	if got := readAsset(t, dir, "go.mk"); got != goMkBefore {
		t.Fatalf("go.mk = %q, want the previous body %q: assets must not change while stale state survives",
			got, goMkBefore)
	}
}

// TestHelperTouchesNothingUnderMakeOnNotModified covers the write-nothing rule
// for a 304 at the level of .make itself, not just the files in it.
//
// The existing 304 test compares the state file's bytes and mtime, and the
// asset bodies. None of that notices a change to the .make DIRECTORY, and a
// lock created and removed inside it changes exactly that on every run,
// including a run that touches no asset. That shipped: measured against real
// codeload, two consecutive warm parses moved .make's mtime while every file
// under it stayed identical.
func TestHelperTouchesNothingUnderMakeOnNotModified(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	if _, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	}); code != 0 {
		t.Fatalf("cold provision exit = %d, want 0: %s", code, stderr)
	}

	// Snapshot every path under .make, directories included, with its mtime.
	makeDir := filepath.Join(dir, ".make")
	snapshot := func() map[string]time.Time {
		seen := map[string]time.Time{}
		if err := filepath.Walk(makeDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			seen[path] = info.ModTime()
			return nil
		}); err != nil {
			t.Fatalf("walk .make: %v", err)
		}
		return seen
	}
	before := snapshot()

	// Wait past the filesystem's timestamp granularity so a rewrite is
	// visible rather than landing in the same tick.
	time.Sleep(1100 * time.Millisecond)

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code != 0 {
		t.Fatalf("warm parse exit = %d, want 0: %s", code, stderr)
	}
	requests := server.Requests()
	if len(requests) != 2 || requests[1].Status != 304 {
		t.Fatalf("second request = %+v, want a 304: this test only means something on the 304 path", requests)
	}

	after := snapshot()
	for path, mod := range before {
		later, present := after[path]
		if !present {
			t.Fatalf("%s disappeared across a 304", path)
		}
		if !later.Equal(mod) {
			t.Fatalf("%s mtime moved across a 304 (%v -> %v); a 304 must write nothing under .make, "+
				"directories included", path, mod, later)
		}
	}
	for path := range after {
		if _, present := before[path]; !present {
			t.Fatalf("%s appeared across a 304; a 304 must write nothing under .make", path)
		}
	}
}

// TestHelperSerializesConcurrentParses covers the lock. Two parses in one
// consumer directory each stage their own archive and install asset by asset,
// so without serialization their copies interleave and .make ends up holding a
// mix of two trees while the state file records whichever finished last. Every
// later run then validates that mixed tree against one archive's ETag, gets a
// 304, and keeps it. Both parses exit 0, so nothing reports it.
//
// The assertion is that .make is internally consistent afterwards: every asset
// comes from the same generation. A mixed tree fails it.
func TestHelperSerializesConcurrentParses(t *testing.T) {
	const parallel = 4
	dir := t.TempDir()

	// Each server serves a distinct generation, so an interleaved install is
	// visible as assets from different generations landing together.
	servers := make([]*fetchServer, parallel)
	for index := range servers {
		generation := fmt.Sprintf("gen%d", index)
		files := map[string]string{
			"go.mk":                      "# go.mk " + generation + "\n",
			"golangci.yml":               "# " + generation + "\n",
			"notices.txt":                generation + "\n",
			"scripts/go-mk-fetch-one.sh": "#!/usr/bin/env bash\n# " + generation + "\nexit 0\n",
			"scripts/go-mk-bin.sh":       "#!/usr/bin/env bash\n# " + generation + "\nexit 0\n",
			"scripts/go-mk-sync.sh":      "#!/usr/bin/env bash\n# " + generation + "\nexit 0\n",
			"scripts/go-mk-bootstrap.sh": "#!/usr/bin/env bash\n# " + generation + "\nexit 0\n",
		}
		servers[index] = newFetchServer(t, files)
	}

	// Each server holds its response for stallPerParse, which makes the
	// critical section long enough to observe directly. Serialized, the four
	// parses cost at least three stalls end to end. Unserialized they overlap
	// and finish in about one.
	//
	// Timing is the assertion rather than a mixed tree, because a mixed tree
	// needs the install loops to interleave, and the install is a handful of
	// small copies. Confirmed: with four parses and no lock, the tree came out
	// consistent every time, so that assertion passed against the very bug it
	// was meant to catch.
	const stallPerParse = 2 * time.Second
	for _, server := range servers {
		server.Stall(stallPerParse)
	}

	// Resolve the helper path on THIS goroutine. repoRootForTest fatals, and
	// the goroutines below must not call anything that does.
	helper := filepath.Join(repoRootForTest(t), "scripts", "go-mk-bootstrap.sh")

	start := time.Now()
	var group sync.WaitGroup
	codes := make([]int, len(servers))
	stderrs := make([]string, len(servers))
	startErrors := make([]error, len(servers))
	for index := range servers {
		group.Add(1)
		go func(slot int, base string) {
			defer group.Done()
			_, stderr, code, err := runHelperFromGoroutine(helper, dir,
				map[string]string{"GO_MK_CODELOAD_BASE": base})
			codes[slot] = code
			stderrs[slot] = stderr
			startErrors[slot] = err
		}(index, servers[index].CodeloadBase())
	}
	group.Wait()

	// Report start failures here, on the test goroutine, rather than from the
	// goroutines that observed them.
	for slot, err := range startErrors {
		if err != nil {
			t.Fatalf("parse %d could not start: %v", slot, err)
		}
	}

	// Every parse must SUCCEED, not merely be serialized. Discarding the exit
	// codes would let "one parse won and the other three waited out the
	// timeout and failed" satisfy both the elapsed-time bound and the
	// consistent-tree check below, which is serialization by rejection rather
	// than by waiting.
	for slot, code := range codes {
		if code != 0 {
			t.Fatalf("parse %d exit = %d, want 0: contenders must wait for the lock and then "+
				"proceed, not be turned away\nstderr: %s", slot, code, stderrs[slot])
		}
	}
	elapsed := time.Since(start)
	t.Logf("%d concurrent parses took %s with a %s stall each", parallel, elapsed, stallPerParse)

	if minimum := 3 * stallPerParse; elapsed < minimum {
		t.Fatalf("%d concurrent parses took %s, want at least %s: they overlapped, "+
			"so provisioning is not serialized and two installs can interleave",
			parallel, elapsed, minimum)
	}

	// Every asset must name the same generation. Which one wins is not
	// specified and does not matter; that they agree is the whole point.
	generations := map[string]string{}
	for _, asset := range []string{
		"go.mk", "golangci.yml", "notices.txt",
		"scripts/go-mk-fetch-one.sh", "scripts/go-mk-bin.sh", "scripts/go-mk-sync.sh",
	} {
		body := readAsset(t, dir, asset)
		found := ""
		for index := range servers {
			if strings.Contains(body, fmt.Sprintf("gen%d", index)) {
				found = fmt.Sprintf("gen%d", index)
			}
		}
		if found == "" {
			t.Fatalf("asset %s = %q, names no generation", asset, body)
		}
		generations[asset] = found
	}
	first := generations["go.mk"]
	for asset, generation := range generations {
		if generation != first {
			t.Fatalf("mixed tree after concurrent parses: go.mk is from %s but %s is from %s; "+
				"two parses interleaved their installs", first, asset, generation)
		}
	}
}

func TestHelperSkipFetchRejectsDirectoryInPlaceOfAsset(t *testing.T) {
	dir := t.TempDir()
	// Seed every required asset, then replace exactly one with a directory.
	// Listing the assets by hand here is what made this test pass for the
	// wrong reason before: the hand-written list omitted
	// scripts/go-mk-bootstrap.sh once that became a required asset, so
	// assets_complete failed on the missing file and never reached the
	// directory at all. Driving the seed from helperFiles keeps the two in
	// step, so the directory really is the only thing wrong with the tree.
	for name, body := range helperFiles() {
		if name == "notices.txt" {
			continue
		}
		writeAsset(t, dir, name, body)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".make", "notices.txt"), 0o755); err != nil {
		t.Fatalf("seed notices.txt as a directory: %v", err)
	}

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_SKIP_FETCH":    "1",
		"GO_MK_CODELOAD_BASE": "http://127.0.0.1:9",
	})
	if code == 0 {
		t.Fatalf("helper exit = 0, want non-zero when a required asset path is a directory rather than a file\nstderr: %s", stderr)
	}
}

// TestHelperDevDirIncompleteFailsLoudly covers GO_MK_DEV_DIR pointing at a
// tree that does not carry every required asset, against an empty .make.
// install_from_dev_dir silently skips any asset missing from the dev dir, so
// main used to return 0 unconditionally afterward, exiting success with an
// incomplete .make.
func TestHelperDevDirIncompleteFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	devDir := t.TempDir()
	writeDevDirAsset(t, devDir, "go.mk", "# dev go.mk\n")
	writeDevDirAsset(t, devDir, "golangci.yml", "version: \"2\"\n")
	// notices.txt and every scripts/* asset are intentionally absent from the
	// dev dir, and .make starts out empty.

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_DEV_DIR": devDir,
	})
	if code == 0 {
		t.Fatalf("helper exit = 0, want non-zero when GO_MK_DEV_DIR does not cover every required asset\nstderr: %s", stderr)
	}
}

func writeDevDirAsset(t *testing.T, devDir string, relative string, body string) {
	t.Helper()
	path := filepath.Join(devDir, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

// testProcessEnvironment builds a complete environment for a spawned make or
// helper by allow-list, naming what the child needs rather than inheriting
// everything and clearing what it must not see.
//
// The inherit-and-clear shape this replaces leaked MAKEFLAGS. This repo's own
// Makefile runs its sub-makes with GO_MK_DEV_DIR="$(CURDIR)" as a make
// command-line variable, make propagates command-line variables to every
// descendant through MAKEFLAGS, and there they OVERRIDE the environment. So
// under `make test` an explicit GO_MK_DEV_DIR= was silently undone for any
// make a test spawned, and four tests measured dev-dir mode instead of the
// path they named: nothing was fetched, request counts read as 0, and a timing
// bound passed in 46ms. They passed locally and failed only in CI.
//
// Clearing MAKEFLAGS by name would fix that one variable. Building the
// environment from scratch fixes the class, because the next ambient variable
// that would change behavior is absent by default instead of leaking until
// someone notices. A variable the child genuinely needs is named here, and a
// missing one surfaces as a loud failure rather than a quiet false pass.
func testProcessEnvironment(overrides map[string]string) []string {
	// PATH finds make, bash, curl, tar, mktemp and awk. HOME and TMPDIR are
	// used for scratch space. Nothing else is required by either child.
	allowed := map[string]string{
		"PATH":   os.Getenv("PATH"),
		"HOME":   os.Getenv("HOME"),
		"TMPDIR": os.Getenv("TMPDIR"),
	}
	for key, value := range overrides {
		allowed[key] = value
	}
	environment := make([]string, 0, len(allowed))
	for key, value := range allowed {
		environment = append(environment, key+"="+value)
	}
	return environment
}

// runHelper executes the helper with the working directory at dir and a
// minimal environment, and returns its output and exit code.
func runHelper(t *testing.T, dir string, env map[string]string) (string, string, int) {
	t.Helper()
	helper := filepath.Join(repoRootForTest(t), "scripts", "go-mk-bootstrap.sh")
	command := exec.Command("bash", helper)
	command.Dir = dir
	defaults := map[string]string{
		"GO_MK_API_REPO": "agoodkind/go-makefile",
		"GO_MK_API_REF":  "main",
	}
	for key, value := range env {
		defaults[key] = value
	}
	command.Env = testProcessEnvironment(defaults)
	stdout, stderr, code, err := runHelperCommand(command)
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	return stdout, stderr, code
}

// runHelperCommand runs the helper and RETURNS a start failure rather than
// calling t.Fatalf, so a caller on another goroutine can carry it back to the
// test goroutine. testing.T's Fatalf is only defined on the goroutine running
// the test: elsewhere it calls runtime.Goexit on the wrong goroutine, so the
// test does not actually stop and the failure can be lost or reported against
// something unrelated. runHelper above is the ordinary same-goroutine path and
// still fatals directly.
func runHelperCommand(command *exec.Cmd) (string, string, int, error) {
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			return stdout.String(), stderr.String(), 0, err
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code, nil
}

// runHelperFromGoroutine is runHelper for a caller that is not the test
// goroutine. It resolves the helper path and environment up front, on the
// caller's goroutine, and returns every failure instead of fataling.
func runHelperFromGoroutine(helper string, dir string, env map[string]string) (string, string, int, error) {
	command := exec.Command("bash", helper)
	command.Dir = dir
	defaults := map[string]string{
		"GO_MK_API_REPO": "agoodkind/go-makefile",
		"GO_MK_API_REF":  "main",
	}
	for key, value := range env {
		defaults[key] = value
	}
	command.Env = testProcessEnvironment(defaults)
	return runHelperCommand(command)
}

func asExitError(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
}

func writeAsset(t *testing.T, dir string, relative string, body string) {
	t.Helper()
	path := filepath.Join(dir, ".make", relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func readAsset(t *testing.T, dir string, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, ".make", relative))
	if err != nil {
		return ""
	}
	return string(body)
}

// repoRootForTest resolves the engine checkout so a test can invoke a script by
// its committed path regardless of the temp working directory.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func TestHelperColdProvisionRecordsState(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0: %s", code, stderr)
	}

	state := readState(t, dir)
	if state["ref"] != "main" {
		t.Fatalf("state ref = %q, want main", state["ref"])
	}
	if state["etag"] == "" {
		t.Fatal("state carried no etag after a cold provision")
	}
	stamp, err := strconv.ParseInt(state["timestamp"], 10, 64)
	if err != nil {
		t.Fatalf("state timestamp %q is not an integer: %v", state["timestamp"], err)
	}
	if time.Since(time.Unix(stamp, 0)) > time.Minute {
		t.Fatalf("state timestamp %d is not recent", stamp)
	}
}

func TestHelperServesDiskWhenUpstreamReturnsNotModified(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	// Cold run populates .make and records the ETag.
	_, _, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("cold run exit = %d, want 0", code)
	}
	// Mark the on-disk copy so a re-extract would be visible.
	writeAsset(t, dir, "go.mk", "# locally marked\n")
	stateBefore := readStateFile(t, dir)
	modTimeBefore := statStateFile(t, dir).ModTime()

	_, stderr, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("warm run exit = %d, want 0: %s", code, stderr)
	}

	if got := readAsset(t, dir, "go.mk"); got != "# locally marked\n" {
		t.Fatalf("go.mk = %q, want the marked body untouched after a 304", got)
	}
	// A 304 must write nothing at all, including the timestamp: the repo
	// owner ruled the reuse window a later task adds runs from the last
	// completed download, not the last successful check, so this compares
	// the whole file byte for byte rather than only the fields a looser
	// check might happen to touch.
	if stateAfter := readStateFile(t, dir); stateAfter != stateBefore {
		t.Fatalf("state file changed across a 304 run:\nbefore: %q\nafter:  %q\nwant byte-identical, a 304 must not rewrite state (not even the timestamp)", stateBefore, stateAfter)
	}
	// The content comparison above would still pass if write_state ran again
	// with the same etag and landed in the same wall-clock second as the
	// cold run, since current_epoch_seconds has one-second resolution. The
	// filesystem's own mtime does not have that blind spot: any write_state
	// call truncates and rewrites the file, so comparing mtime catches a
	// same-second rewrite that a content-only comparison could miss.
	if modTimeAfter := statStateFile(t, dir).ModTime(); !modTimeAfter.Equal(modTimeBefore) {
		t.Fatalf("state file mtime changed across a 304 run: before %v, after %v, want unchanged (a 304 must not touch the state file at all)", modTimeBefore, modTimeAfter)
	}
	requests := server.Requests()
	if len(requests) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(requests))
	}
	if requests[1].Method != "HEAD" {
		t.Fatalf("second request method = %q, want HEAD (the validation probe must not GET and discard the tarball body)", requests[1].Method)
	}
	if requests[1].Status != 304 {
		t.Fatalf("second request status = %d, want 304", requests[1].Status)
	}
	if requests[1].Bytes != 0 {
		t.Fatalf("second request transferred %d bytes, want 0", requests[1].Bytes)
	}
}

// readStateFile reads the raw fetch-state bytes so a test can assert the
// file is byte-identical across a run, catching a state write that only
// moves the timestamp while leaving every parsed field looking unchanged.
func readStateFile(t *testing.T, dir string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, ".make", ".go-mk-fetch-state"))
	if err != nil {
		t.Fatalf("read fetch state: %v", err)
	}
	return string(body)
}

// statStateFile stats the fetch-state file so a test can compare its mtime
// across a run, catching a rewrite that a content comparison alone could
// miss when it lands within the same wall-clock second as the prior write.
func statStateFile(t *testing.T, dir string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, ".make", ".go-mk-fetch-state"))
	if err != nil {
		t.Fatalf("stat fetch state: %v", err)
	}
	return info
}

func TestHelperReprovisionsWhenUpstreamMoved(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	_, _, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("cold run exit = %d, want 0", code)
	}
	firstState := readState(t, dir)

	moved := helperFiles()
	moved["go.mk"] = "# go.mk v2\n"
	server.SetFiles(moved)

	_, stderr, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("moved run exit = %d, want 0: %s", code, stderr)
	}

	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v2\n" {
		t.Fatalf("go.mk = %q, want the advanced body", got)
	}
	secondState := readState(t, dir)
	if secondState["etag"] == firstState["etag"] {
		t.Fatal("state etag did not change after upstream moved")
	}
}

// TestHelperDegradesWhenUpstreamServesNoETag covers a 200 response that
// carries no ETag header at all. A prior fix (reverted here) made this a
// hard provisioning failure; a reviewer argued that failure mode is worse
// than the bug it prevented, since it would break every consumer's build
// the moment codeload ever stopped sending ETag on archive responses,
// leaving a cold consumer with no .make at all rather than a working
// engine. The repo owner agreed: the tree is already verified and installed
// by the point the ETag is missing, so this now installs it, skips the
// state write (so a later run finds no etag and downloads unconditionally
// rather than validating against content this run never confirmed), warns
// loudly, and still returns success.
func TestHelperDegradesWhenUpstreamServesNoETag(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	server.SetETagEnabled(false)
	dir := t.TempDir()

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0 when upstream serves a 200 with no ETag header (degrade, don't fail): %s", code, stderr)
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the fetched tree installed even without an ETag", got)
	}
	if !strings.Contains(stderr, "ETag") {
		t.Fatalf("stderr = %q, want it to warn about the missing ETag", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".make", ".go-mk-fetch-state")); err == nil {
		t.Fatal("state file exists after a provision that had no ETag to record")
	}
}

// TestHelperClearsStaleStateWhenUpstreamStopsServingETag covers the
// transition case: a consumer already has valid state from an earlier
// provision that did carry an ETag, and upstream then stops sending one. A
// naive "just skip the write" leaves the old etag and timestamp in place,
// so a later run would still "find" an etag, exactly the outcome the fix
// above is meant to prevent (the currently-installed content was never
// verified against that leftover etag). The fix must remove the stale
// state file, not merely skip writing a new one.
func TestHelperClearsStaleStateWhenUpstreamStopsServingETag(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	_, _, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("cold run exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".make", ".go-mk-fetch-state")); err != nil {
		t.Fatalf("state file missing after a cold run that did carry an ETag: %v", err)
	}

	moved := helperFiles()
	moved["go.mk"] = "# go.mk v2\n"
	server.SetFiles(moved)
	server.SetETagEnabled(false)

	_, stderr, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("no-etag run exit = %d, want 0: %s", code, stderr)
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v2\n" {
		t.Fatalf("go.mk = %q, want the newly fetched tree installed", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".make", ".go-mk-fetch-state")); err == nil {
		t.Fatal("state file still exists after upstream stopped serving an ETag, want it removed rather than left stale")
	}
}

// TestHelperDoesNotReuseETagAcrossRef covers a consumer that changes
// GO_MK_API_REF between runs. Before this fix, the stored etag was reused
// regardless of which ref it was recorded against, so a run against a new
// ref could validate a 304 against content that was never actually fetched
// for that ref. The fix must skip the stored etag whenever the stored ref
// does not match the current ref, forcing a real fetch instead of a
// conditional probe.
func TestHelperDoesNotReuseETagAcrossRef(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	_, _, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GO_MK_API_REF":       "main",
	})
	if code != 0 {
		t.Fatalf("cold run exit = %d, want 0", code)
	}

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GO_MK_API_REF":       "other-ref",
	})
	if code != 0 {
		t.Fatalf("ref-changed run exit = %d, want 0: %s", code, stderr)
	}

	requests := server.Requests()
	if len(requests) != 2 {
		t.Fatalf("server saw %d requests, want 2 (a changed ref must skip the validation probe and fetch fresh)", len(requests))
	}
	if requests[1].Method != "GET" {
		t.Fatalf("second request method = %q, want GET (a changed ref must go straight to provision, not a HEAD validation probe)", requests[1].Method)
	}
	if requests[1].IfNoneMatch != "" {
		t.Fatalf("second request If-None-Match = %q, want empty (the stored etag belongs to a different ref and must not be reused)", requests[1].IfNoneMatch)
	}
	// The ref must reach the URL, not just the state file. Without this a
	// helper that recorded the new ref while still fetching the old one would
	// satisfy every other assertion in this test.
	if want := "/agoodkind/go-makefile/tar.gz/main"; requests[0].Path != want {
		t.Fatalf("first request path = %q, want %q", requests[0].Path, want)
	}
	if want := "/agoodkind/go-makefile/tar.gz/other-ref"; requests[1].Path != want {
		t.Fatalf("second request path = %q, want %q (the changed ref never reached the URL)", requests[1].Path, want)
	}

	secondState := readState(t, dir)
	if secondState["ref"] != "other-ref" {
		t.Fatalf("state ref = %q, want other-ref", secondState["ref"])
	}
}

func readState(t *testing.T, dir string) map[string]string {
	t.Helper()
	file, err := os.Open(filepath.Join(dir, ".make", ".go-mk-fetch-state"))
	if err != nil {
		t.Fatalf("open fetch state: %v", err)
	}
	defer func() { _ = file.Close() }()
	state := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if found {
			state[key] = value
		}
	}
	return state
}

func writeState(t *testing.T, dir string, ref string, etag string, timestamp int64) {
	t.Helper()
	body := fmt.Sprintf("ref=%s\netag=%s\ntimestamp=%d\n", ref, etag, timestamp)
	path := filepath.Join(dir, ".make", ".go-mk-fetch-state")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for state: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

// warmMake populates .make with a complete asset set so only the validation
// decision is under test. "Complete" means every entry required_assets lists,
// including scripts/go-mk-bootstrap.sh: the helper installs its own successor,
// so that file is a required asset, and omitting it leaves assets_complete
// false. A .make that fails assets_complete never reaches the state read, so
// the helper skips validate_upstream entirely and provisions instead, which
// silently turns any test of the validation decision into a test of the
// provision path.
func warmMake(t *testing.T, dir string) {
	t.Helper()
	for name, body := range helperFiles() {
		writeAsset(t, dir, name, body)
	}
}

func TestHelperServesDiskWhenUpstreamTimesOutAndStateIsRecent(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(-10*time.Minute).Unix())
	server.Stall(5 * time.Second)
	stateBefore := readStateFile(t, dir)
	modTimeBefore := statStateFile(t, dir).ModTime()

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0 when state is recent: %s", code, stderr)
	}
	if !strings.Contains(stderr, "serving .make assets validated") {
		t.Fatalf("stderr = %q, want a warning naming the served assets", stderr)
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the warm body preserved", got)
	}
	// The reuse window is fixed from the last completed download, not the
	// last successful reuse: a write here would let the window slide
	// forward on every offline run, turning a fixed hour into unbounded
	// reuse while the rest of the suite stayed green. Comparing both the
	// raw bytes and the mtime (the same pair TestHelperServesDiskWhenUpstreamReturnsNotModified
	// uses for the 304 case) catches a rewrite that lands in the same
	// wall-clock second as the original write, which a content-only
	// comparison could miss.
	if stateAfter := readStateFile(t, dir); stateAfter != stateBefore {
		t.Fatalf("state file changed across a reuse run:\nbefore: %q\nafter:  %q\nwant byte-identical, reuse must not rewrite state", stateBefore, stateAfter)
	}
	if modTimeAfter := statStateFile(t, dir).ModTime(); !modTimeAfter.Equal(modTimeBefore) {
		t.Fatalf("state file mtime changed across a reuse run: before %v, after %v, want unchanged", modTimeBefore, modTimeAfter)
	}
}

// unreachableCodeloadBase returns an address nothing is listening on, so
// every request fails at connect (curl exit 7) rather than merely stalling.
// It opens a real listener, reads its address, and closes it immediately,
// rather than pointing at a fixed low port number: this suite runs on a
// real developer machine rather than an isolated network namespace, so a
// hardcoded port carries a small but real risk of an unrelated process
// already holding it, while a port that this call just held and released
// is refused deterministically. This is the easy shape of network failure:
// curl's default --retry policy does not even retry a connection refusal
// (only a timeout or a 5xx/4xx response counts as transient by default),
// so this fails on the first attempt.
func unreachableCodeloadBase(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open a listener to find a free port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return "http://" + address
}

// TestHelperFailsWhenUpstreamTimesOutAndStateIsStale covers the harder,
// more realistic shape of network failure: an upstream that accepts the
// connection and then never responds, as a flaky network, a captive
// portal, or a hung proxy actually produces. A stale state falls through
// past the reuse branch into a real provision() attempt, whose curl call
// now aborts a stalled transfer by lack of progress
// (FETCH_SPEED_LIMIT/FETCH_SPEED_TIME) rather than by riding out
// FETCH_MAX_TIME, so each attempt dies in a few seconds regardless of how
// long the stall actually lasts; --retry still applies on top of that (see
// TestHelperBoundsRetryTimeWhenUpstreamStalls for the measured total).
func TestHelperFailsWhenUpstreamTimesOutAndStateIsStale(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(-2*time.Hour).Unix())
	// Longer than FETCH_SPEED_TIME (3s) so every attempt's speed-limit abort
	// fires from lack of progress rather than the stall happening to end
	// first; how much longer does not matter, since each retry opens a
	// fresh connection and the server sleeps again from scratch.
	server.Stall(10 * time.Second)

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code == 0 {
		t.Fatal("helper exit = 0, want non-zero when state is older than the reuse window")
	}
	if strings.Contains(stderr, "serving .make assets validated") {
		t.Fatalf("stderr = %q, want no serve warning on the stale path", stderr)
	}
	// The validation probe's own failure reason (curl's exit code and an
	// excerpt of its stderr) must stay visible on this fall-through path,
	// not just on the reuse-serving path: a probe failing on every run is
	// otherwise invisible once main proceeds straight to provision.
	if !strings.Contains(stderr, "validate_upstream: curl exited") {
		t.Fatalf("stderr = %q, want the validation probe's own failure reason on the stale fall-through path", stderr)
	}
	// Even on the failing path, nothing may be destroyed.
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the warm body preserved through a failure", got)
	}
}

// TestHelperTreatsFutureTimestampAsStale covers the connection-refused
// shape (see unreachableCodeloadBase); TestHelperFailsWhenUpstreamTimesOutAndStateIsStale
// covers the stall shape, so between the two the suite exercises both ways
// a real network failure reaches this branch.
func TestHelperTreatsFutureTimestampAsStale(t *testing.T) {
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(2*time.Hour).Unix())

	_, _, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": unreachableCodeloadBase(t),
	})
	if code == 0 {
		t.Fatal("helper exit = 0, want non-zero for a future timestamp")
	}
}

// TestHelperBoundsRetryTimeWhenUpstreamStalls covers a cold provision (no
// prior state, so main goes straight to provision rather than through
// validate_upstream) against an upstream that accepts the connection and
// then never sends anything further. provision's curl now aborts a stalled
// transfer by lack of progress (--speed-limit/--speed-time), not by waiting
// out --max-time, so a single attempt dies in about FETCH_SPEED_TIME (3s)
// regardless of how long the stall actually lasts. --retry 3 --retry-delay 2
// still applies on top of that (curl treats a speed-limit abort as a
// retriable transient error, the same as it did a --max-time expiry), and
// --retry-max-time (FETCH_RETRY_MAX_TIME) caps that cascade at two attempts:
// measured locally at ~8s (2 attempts x ~3s + 1 retry delay x 2s), against
// ~18s with --retry uncapped, 30-62s in an earlier --retry-max-time-only fix
// at a larger value, and 134s before any progress-based abort existed. A
// test that only checks the exit code would pass at any of those costs, so
// this asserts a wall-clock ceiling tuned to the current architecture, not
// just eventual failure.
func TestHelperBoundsRetryTimeWhenUpstreamStalls(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	// Long enough that the speed-limit abort (not the stall ending) is what
	// stops each attempt; kept short (rather than merely "longer than
	// FETCH_SPEED_TIME") because the test server's own handler goroutines
	// keep sleeping for the full stall duration even after curl gives up
	// client-side, and the httptest.Server's Close (run via t.Cleanup)
	// blocks on each of those goroutines unwinding, so a needlessly long
	// stall here inflates the test's reported wall time well past what the
	// helper itself actually takes.
	server.Stall(10 * time.Second)

	start := time.Now()
	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	elapsed := time.Since(start)
	// Logged unconditionally (not just on failure) because the overall test
	// duration Go reports also includes the fetch server's cleanup, which
	// blocks until the stalled connection's own goroutine unwinds; that is
	// unrelated to the bound this test is asserting.
	t.Logf("helper took %s against a stalled upstream", elapsed)

	if code == 0 {
		t.Fatalf("helper exit = 0, want non-zero against a stalled upstream: %s", stderr)
	}
	// Measured worst case is ~8s (2 attempts x ~3s + 1 retry delay x 2s).
	// 15s gives margin for CI/host jitter while staying well clear of the
	// ~18s an uncapped retry cascade would cost, so a regression back to
	// that shape would still be caught.
	const wallClockCeiling = 15 * time.Second
	if elapsed > wallClockCeiling {
		t.Fatalf("helper took %s against a stalled upstream, want under %s (a retried stall is not bounded)", elapsed, wallClockCeiling)
	}
}

// TestHelperCompletesWhenUpstreamTricklesAboveSpeedLimit covers the other
// side of the progress-based abort: a transfer that is genuinely slow but
// never stops making progress must not be punished by FETCH_SPEED_LIMIT/
// FETCH_SPEED_TIME the way a real stall now is. Without this test, a later
// tightening of those numbers (or of FETCH_MAX_TIME) could start failing
// good networks silently, since every other test in this file exercises
// either a fast, healthy transfer or a fully stalled one.
//
// notices.txt is inflated to pseudo-random, close-to-incompressible content
// so the served tarball's compressed size is large enough that trickling it
// at 2 KB/s takes several seconds (comfortably more than one
// FETCH_SPEED_TIME window, so the abort check gets multiple chances to
// wrongly fire) rather than finishing as soon as gzip collapses
// realistic-sized padding down to nothing. 12 KB was chosen over a larger
// size specifically to keep this well clear of FETCH_MAX_TIME (15s): an
// earlier 18 KB body measured ~9.6s, only about 5s of margin before
// ordinary CI/host jitter could read as a false max-time regression; 12 KB
// measures ~5-6s, leaving roughly 9-10s of margin instead.
func TestHelperCompletesWhenUpstreamTricklesAboveSpeedLimit(t *testing.T) {
	files := helperFiles()
	files["notices.txt"] = randomAssetBody(12 * 1024)
	server := newFetchServer(t, files)
	dir := t.TempDir()
	const trickleBytesPerSecond = 2048
	server.Trickle(trickleBytesPerSecond)

	start := time.Now()
	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	elapsed := time.Since(start)
	t.Logf("helper took %s against a %d B/s trickling upstream", elapsed, trickleBytesPerSecond)

	if code != 0 {
		t.Fatalf("helper exit = %d, want 0 against a slow but progressing upstream: %s", code, stderr)
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the fetched tree installed", got)
	}
	if got := readAsset(t, dir, "notices.txt"); got != files["notices.txt"] {
		t.Fatal("notices.txt does not match the trickled body, want the full slow transfer to have landed intact")
	}
}

// TestHelperDoesNotReuseWhenLocalSetupFailsBeforeAnyRequest covers
// validate_upstream failing before it ever attempts a request (an
// unwritable TMPDIR breaking its own mktemp), with a recent, otherwise
// reuse-eligible state and a genuinely healthy upstream. If a local setup
// failure were treated the same as a real network failure, this would
// wrongly serve the warm .make tree with an "upstream unreachable" warning,
// even though the network was never consulted and, in this scenario, is
// not the problem at all. The fix must skip reuse for this case and fall
// through to provision, which fails loudly with its own local-cause
// message when the same broken TMPDIR blocks its staging directory too.
func TestHelperDoesNotReuseWhenLocalSetupFailsBeforeAnyRequest(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(-10*time.Minute).Unix())

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"TMPDIR":              filepath.Join(dir, "no-such-tmpdir"),
	})
	if code == 0 {
		t.Fatalf("helper exit = 0, want non-zero when TMPDIR is broken: %s", stderr)
	}
	if strings.Contains(stderr, "upstream unreachable") {
		t.Fatalf("stderr = %q, want no upstream-unreachable warning: the network was never consulted, only a local setup failure occurred", stderr)
	}
	// Assert that a local cause is named, not which function names it. This
	// used to require validate_upstream's and provision's specific wording,
	// which pinned the internal call order rather than the property: the lock
	// now runs before either of them and rejects a broken TMPDIR first, so
	// those two never execute. Failing earlier with the real reason is the
	// better outcome, and the rule that matters is unchanged.
	//
	// The path itself is the discriminator. A regression that reported a
	// generic network complaint would satisfy a mere "some error appeared"
	// check but not this one.
	brokenTmp := filepath.Join(dir, "no-such-tmpdir")
	if !strings.Contains(stderr, brokenTmp) {
		t.Fatalf("stderr = %q, want it to name the broken TMPDIR %q: a local setup failure must "+
			"report its own cause rather than a generic network complaint", stderr, brokenTmp)
	}
	if !strings.Contains(stderr, "local setup problem") && !strings.Contains(stderr, "not a lock conflict") {
		t.Fatalf("stderr = %q, want it to say the failure is local rather than remote", stderr)
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the warm body preserved through a failure", got)
	}
}

// TestHelperAlwaysProvisionsUnconditionallyInCI covers a CI run (both
// GITHUB_ACTIONS=true and a GITHUB_RUN_ID present) against otherwise
// reuse-eligible state: a fresh timestamp that would produce a 304 (and,
// if the probe instead failed, a disk serve) outside CI. In CI, none of
// that conditional machinery may run at all: the request must carry no
// If-None-Match and must be a real 200, not a 304 the helper then trusts
// without having downloaded anything this run.
func TestHelperAlwaysProvisionsUnconditionallyInCI(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	// Fresh state that would produce a 304 and a disk serve off CI.
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Unix())

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GITHUB_ACTIONS":      "true",
		"GITHUB_RUN_ID":       "1",
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0: %s", code, stderr)
	}

	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(requests))
	}
	if requests[0].IfNoneMatch != "" {
		t.Fatalf("CI request carried If-None-Match %q, want none", requests[0].IfNoneMatch)
	}
	if requests[0].Status != 200 {
		t.Fatalf("CI request status = %d, want 200", requests[0].Status)
	}
	// Assert a real body came back, not just a 200-shaped response. Without
	// this a regression that answered from disk while still issuing one
	// unconditional request would satisfy every assertion above.
	if requests[0].Bytes == 0 {
		t.Fatal("CI request transferred 0 bytes, want the full archive: CI must re-download, not serve disk")
	}
	// On what this cannot cover: the rule is also that CI never READS the
	// state file, and a read whose value is then discarded has no observable
	// effect from outside the process, so no assertion here can distinguish
	// it. What is observable is every way such a read could change behavior,
	// and those are covered: a read that reached the request shows up as
	// If-None-Match above, and a read that enabled reuse shows up as a
	// success against an unreachable upstream in
	// TestHelperFailsInCIWhenUpstreamIsUnreachableDespiteFreshState.
}

// TestHelperFailsInCIWhenUpstreamIsUnreachableDespiteFreshState covers the
// consequence of the CI guard above: with reuse and conditional validation
// both disabled in CI, a fresh, otherwise-reuse-eligible state does not
// soften an unreachable upstream. CI must fail loudly rather than silently
// serving assets that were never re-validated this run.
func TestHelperFailsInCIWhenUpstreamIsUnreachableDespiteFreshState(t *testing.T) {
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Unix())

	_, _, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": "http://127.0.0.1:9",
		"GITHUB_ACTIONS":      "true",
		"GITHUB_RUN_ID":       "1",
	})
	if code == 0 {
		t.Fatal("helper exit = 0 in CI with an unreachable upstream, want non-zero")
	}
}

// TestHelperTreatsGithubActionsWithoutRunIdAsLocal covers the other half
// of running_in_ci's contract: GITHUB_ACTIONS alone (no GITHUB_RUN_ID) is
// not a CI run, since a local shell that happens to export GITHUB_ACTIONS
// (or a consumer testing CI-adjacent tooling locally) must still get
// ordinary conditional-validation behavior, not the unconditional-fetch
// path meant for a real GitHub Actions job.
func TestHelperTreatsGithubActionsWithoutRunIdAsLocal(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	_, _, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("cold run exit = %d, want 0", code)
	}

	_, _, code = runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GITHUB_ACTIONS":      "true",
		// No GITHUB_RUN_ID, so this is not a CI run.
	})
	if code != 0 {
		t.Fatalf("second run exit = %d, want 0", code)
	}

	requests := server.Requests()
	if requests[len(requests)-1].Status != 304 {
		t.Fatalf("last status = %d, want 304 because GITHUB_ACTIONS alone is not CI",
			requests[len(requests)-1].Status)
	}
}
