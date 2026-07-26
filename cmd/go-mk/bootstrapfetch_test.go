package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
