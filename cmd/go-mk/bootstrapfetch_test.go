package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
	if requests := server.Requests(); len(requests) != 1 {
		t.Fatalf("server saw %d requests, want exactly 1", len(requests))
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
		// A port nothing listens on, so every request fails at connect.
		"GO_MK_CODELOAD_BASE": "http://127.0.0.1:9",
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

// TestHelperMidInstallFailureExitsNonZero forces install_from_stage's cp to
// fail on a required asset that sits neither first nor last in
// required_assets()'s order (golangci.yml, the second of six), by
// pre-creating it read-only so cp cannot overwrite it. The assets before it
// (go.mk) and after it (notices.txt, every scripts/* asset) install
// successfully. Before this fix, the subshell running provision's staging
// work executes with errexit suppressed (it inherits that from being called
// as the condition of an if), so install_from_stage's implicit return value
// was just whatever its last loop iteration happened to return; since that
// last iteration (scripts/go-mk-sync.sh) still succeeds here, a naive test
// that breaks the last asset instead of a middle one would pass even with
// the bug present. Breaking a middle asset is what actually distinguishes
// "every step's failure propagates" from "only the last step's failure
// happens to be visible".
func TestHelperMidInstallFailureExitsNonZero(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	makeDir := filepath.Join(dir, ".make")
	if err := os.MkdirAll(makeDir, 0o755); err != nil {
		t.Fatalf("mkdir .make: %v", err)
	}
	golangciPath := filepath.Join(makeDir, "golangci.yml")
	if err := os.WriteFile(golangciPath, []byte("stale, read-only\n"), 0o444); err != nil {
		t.Fatalf("seed read-only golangci.yml: %v", err)
	}

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code == 0 {
		t.Fatalf("helper exit = 0, want non-zero when a middle install step (golangci.yml) fails while the surrounding steps succeed\nstderr: %s", stderr)
	}
}

// TestHelperSkipFetchRejectsDirectoryInPlaceOfAsset covers assets_complete
// against a tree where a required asset path is actually a directory. The -s
// test alone is true for a directory (it has nonzero apparent size), so a
// broken tree like this used to pass as complete.
func TestHelperSkipFetchRejectsDirectoryInPlaceOfAsset(t *testing.T) {
	dir := t.TempDir()
	writeAsset(t, dir, "go.mk", "# warm go.mk\n")
	writeAsset(t, dir, "golangci.yml", "version: \"2\"\n")
	writeAsset(t, dir, "scripts/go-mk-fetch-one.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-bin.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-sync.sh", "#!/usr/bin/env bash\nexit 0\n")
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

// runHelper executes the helper with the working directory at dir and a
// minimal environment, and returns its output and exit code.
func runHelper(t *testing.T, dir string, env map[string]string) (string, string, int) {
	t.Helper()
	helper := filepath.Join(repoRootForTest(t), "scripts", "go-mk-bootstrap.sh")
	command := exec.Command("bash", helper)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"GO_MK_API_REPO=agoodkind/go-makefile",
		"GO_MK_API_REF=main",
		"GO_MK_DEV_DIR=",
		"GO_MK_MODULES=",
		// Never let the test inherit a real CI environment.
		"GITHUB_ACTIONS=",
		"GITHUB_RUN_ID=",
	)
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			t.Fatalf("run helper: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
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
// decision is under test.
func warmMake(t *testing.T, dir string) {
	t.Helper()
	for name, body := range helperFiles() {
		if name == "scripts/go-mk-bootstrap.sh" {
			continue
		}
		writeAsset(t, dir, name, body)
	}
}

func TestHelperServesDiskWhenUpstreamTimesOutAndStateIsRecent(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(-10*time.Minute).Unix())
	server.Stall(5 * time.Second)

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
}

// unreachableCodeloadBase is a port nothing listens on, so every request
// fails at connect (curl exit 7) rather than merely stalling. This is the
// easy shape of network failure: curl's default --retry policy does not
// even retry a connection refusal (only a timeout or a 5xx/4xx response
// counts as transient by default), so this fails on the first attempt.
const unreachableCodeloadBase = "http://127.0.0.1:9"

// TestHelperFailsWhenUpstreamTimesOutAndStateIsStale covers the harder,
// more realistic shape of network failure: an upstream that accepts the
// connection and then never responds, as a flaky network, a captive
// portal, or a hung proxy actually produces. A stale state falls through
// past the reuse branch into a real provision() attempt, whose curl call
// carries --retry 3 --retry-delay 2 and --retry-max-time
// (FETCH_RETRY_MAX_TIME, below FETCH_MAX_TIME): a stall this long used to
// cost roughly 4x FETCH_MAX_TIME once retries ran unbounded (measured at
// 134s before that fix), but is now bounded to one ~FETCH_MAX_TIME attempt.
func TestHelperFailsWhenUpstreamTimesOutAndStateIsStale(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(-2*time.Hour).Unix())
	// Longer than FETCH_MAX_TIME (30s) so provision's one permitted attempt
	// genuinely exhausts its own timeout rather than completing early.
	server.Stall(40 * time.Second)

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
		"GO_MK_CODELOAD_BASE": unreachableCodeloadBase,
	})
	if code == 0 {
		t.Fatal("helper exit = 0, want non-zero for a future timestamp")
	}
}

// TestHelperBoundsRetryTimeWhenUpstreamStalls covers a cold provision (no
// prior state, so main goes straight to provision rather than through
// validate_upstream) against an upstream that accepts the connection and
// then never responds. provision's curl carries --retry 3 --retry-delay 2,
// and curl treats its own --max-time expiry as a retriable transient error;
// without a --retry-max-time cap, each of up to 4 attempts (the initial one
// plus 3 retries) can independently run the full FETCH_MAX_TIME, so a
// single stalled upstream can cost roughly 4x FETCH_MAX_TIME plus retry
// delays. A test that only checks the exit code would pass at that cost
// too, so this asserts a wall-clock ceiling well under the unbounded-retry
// cost, not just eventual failure.
func TestHelperBoundsRetryTimeWhenUpstreamStalls(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	// Longer than FETCH_MAX_TIME (30s) so the single permitted attempt
	// genuinely exhausts its own timeout rather than completing early.
	server.Stall(40 * time.Second)

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
	// FETCH_MAX_TIME (30s) bounds a single attempt; FETCH_RETRY_MAX_TIME
	// (20s), being less than FETCH_MAX_TIME, must prevent a second attempt
	// from ever starting once the first has already used the whole budget.
	// 45s gives headroom over the ~30s expected cost without coming close to
	// the ~62s+ a single additional retried attempt would add back.
	const wallClockCeiling = 45 * time.Second
	if elapsed > wallClockCeiling {
		t.Fatalf("helper took %s against a stalled upstream, want under %s (a retried timeout is not bounded)", elapsed, wallClockCeiling)
	}
}
