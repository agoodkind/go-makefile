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

func TestHelperLeavesAssetsIntactWhenUpstreamIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	writeAsset(t, dir, "go.mk", "# warm go.mk\n")
	writeAsset(t, dir, "golangci.yml", "version: \"2\"\n")
	writeAsset(t, dir, "notices.txt", "warm notice\n")
	writeAsset(t, dir, "scripts/go-mk-fetch-one.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-bin.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-sync.sh", "#!/usr/bin/env bash\nexit 0\n")

	_, _, code := runHelper(t, dir, map[string]string{
		// A port nothing listens on, so every request fails at connect.
		"GO_MK_CODELOAD_BASE": "http://127.0.0.1:9",
	})
	if code == 0 {
		t.Fatal("helper exit = 0, want non-zero with no state and an unreachable upstream")
	}

	// The point of the task: a failed fetch must not remove what was there.
	if got := readAsset(t, dir, "go.mk"); got != "# warm go.mk\n" {
		t.Fatalf("go.mk = %q after a failed fetch, want the original body preserved", got)
	}
	if got := readAsset(t, dir, "golangci.yml"); got == "" {
		t.Fatal("golangci.yml was destroyed by a failed fetch")
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
