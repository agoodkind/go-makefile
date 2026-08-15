package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveBinRebuildsWhenSourceNewer(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, ".make", "go-mk")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(output, []byte("stale"), 0o755); err != nil {
		t.Fatalf("write stale binary: %v", err)
	}
	staleTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(output, staleTime, staleTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	repo := t.TempDir()
	cmdDir := filepath.Join(repo, "cmd", "go-mk")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.invalid/engine\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	t.Setenv("GO_MK_BIN", "")
	t.Setenv("_GO_MK_ROOT", dir)
	t.Setenv("GO_MK_ROOT", "")
	t.Setenv("GO_MK_BUILD_REPO", repo)
	t.Setenv("GO_MK_BUILD_PKG", "./cmd/go-mk")

	if err := resolveEngineBinary(); err != nil {
		t.Fatalf("resolveEngineBinary: %v", err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("stat rebuilt binary: %v", err)
	}
	if info.Size() == int64(len("stale")) {
		t.Fatalf("cached binary still %d bytes, want a rebuilt go-mk", info.Size())
	}
	if info.ModTime().Equal(staleTime) {
		t.Fatal("cached binary mtime unchanged, want a rebuild from GO_MK_BUILD_REPO")
	}
}
