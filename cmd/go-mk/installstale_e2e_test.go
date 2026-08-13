package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installEngineMarker is the engine invocation go-build.mk's $(INSTALL_BIN)
// rule runs. Its presence in a dry run is the observable that says make decided
// the installed binary is stale.
const installEngineMarker = `go-mk" install`

// newStaleConsumer writes a minimal binary-mode consumer that includes the
// repository's own go.mk and go-build.mk, and returns its directory.
func newStaleConsumer(t *testing.T) string {
	t.Helper()
	repoRoot := repoRootForTest(t)
	consumerDir := t.TempDir()

	makeDir := filepath.Join(consumerDir, ".make")
	if err := os.MkdirAll(makeDir, 0o755); err != nil {
		t.Fatalf("create consumer .make: %v", err)
	}
	if err := os.WriteFile(filepath.Join(makeDir, "golangci.yml"), []byte("version: \"2\"\n"), 0o644); err != nil {
		t.Fatalf("seed golangci configuration: %v", err)
	}
	// GO_MK_SKIP_FETCH requires every opted-in module to be vendored already.
	if err := os.Symlink(filepath.Join(repoRoot, "go-build.mk"), filepath.Join(makeDir, "go-build.mk")); err != nil {
		t.Fatalf("vendor go-build.mk: %v", err)
	}

	cmdDir := filepath.Join(consumerDir, "cmd", "demo")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("create consumer command directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write consumer main: %v", err)
	}
	if err := os.WriteFile(filepath.Join(consumerDir, "go.mod"), []byte("module demo\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write consumer go.mod: %v", err)
	}

	makefile := "GO_MK_DEV_DIR := " + repoRoot + "\n" +
		"GO_MK_SKIP_FETCH := 1\n" +
		"GO_MK_MODULES := go-build.mk\n" +
		"BINARY := demo\n" +
		"CMD := ./cmd/demo\n" +
		"INSTALL_DIR := " + filepath.Join(consumerDir, "bin") + "\n" +
		"include " + filepath.Join(repoRoot, "go.mk") + "\n"
	if err := os.WriteFile(filepath.Join(consumerDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write consumer Makefile: %v", err)
	}
	return consumerDir
}

// dryRunInstall asks make what `install` would do without running it, which is
// the same staleness decision a real run makes.
func dryRunInstall(t *testing.T, consumerDir string) string {
	t.Helper()
	command := exec.Command("make", "-n", "install")
	command.Dir = consumerDir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n install: %v\n%s", err, output)
	}
	return string(output)
}

// touch dates a path forward so make sees it as newer than everything written
// before it, without the test sleeping for real clock movement.
func touch(t *testing.T, path string, offset time.Duration) {
	t.Helper()
	stamp := time.Now().Add(offset)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

func TestInstallRebuildsOnlyWhenSourcesAreNewer(t *testing.T) {
	consumerDir := newStaleConsumer(t)
	installedBin := filepath.Join(consumerDir, "bin", "demo")
	mainGo := filepath.Join(consumerDir, "cmd", "demo", "main.go")
	lintConfig := filepath.Join(consumerDir, ".make", "golangci.yml")

	output := dryRunInstall(t, consumerDir)
	if !strings.Contains(output, installEngineMarker) {
		t.Fatalf("missing installed binary should install; got:\n%s", output)
	}
	// The codegen and workspace prerequisites must be ordered before the
	// compile, not merely before the install wrapper.
	workspaceIndex := strings.Index(output, "go-mk-workspace")
	engineIndex := strings.Index(output, installEngineMarker)
	if workspaceIndex < 0 || workspaceIndex > engineIndex {
		t.Fatalf("go-mk-workspace must run before the engine install; got:\n%s", output)
	}

	if err := os.MkdirAll(filepath.Dir(installedBin), 0o755); err != nil {
		t.Fatalf("create install directory: %v", err)
	}
	if err := os.WriteFile(installedBin, []byte("binary\n"), 0o755); err != nil {
		t.Fatalf("write installed binary: %v", err)
	}
	touch(t, installedBin, time.Hour)

	output = dryRunInstall(t, consumerDir)
	if strings.Contains(output, installEngineMarker) {
		t.Fatalf("fresh installed binary should not reinstall; got:\n%s", output)
	}

	touch(t, mainGo, 2*time.Hour)
	output = dryRunInstall(t, consumerDir)
	if !strings.Contains(output, installEngineMarker) {
		t.Fatalf("changed source should reinstall; got:\n%s", output)
	}

	touch(t, installedBin, 3*time.Hour)
	touch(t, lintConfig, 4*time.Hour)
	output = dryRunInstall(t, consumerDir)
	if !strings.Contains(output, installEngineMarker) {
		t.Fatalf("changed lint config should reinstall; got:\n%s", output)
	}

	touch(t, installedBin, 5*time.Hour)
	if err := os.Remove(installedBin); err != nil {
		t.Fatalf("remove installed binary: %v", err)
	}
	output = dryRunInstall(t, consumerDir)
	if !strings.Contains(output, installEngineMarker) {
		t.Fatalf("removed installed binary should reinstall; got:\n%s", output)
	}
}
