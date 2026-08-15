package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"goodkind.io/gklog/correlation"
)

func TestCacheManifestIsHeaderless(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	os.Args = []string{"go-mk", "cache-manifest"}
	if !headerless() {
		t.Fatal("cache-manifest should not print a run header")
	}

	os.Args = []string{"go-mk", "lint"}
	if headerless() {
		t.Fatal("lint should print a run header")
	}
}

func TestRunHeaderHasNoLeadingSymbol(t *testing.T) {
	corr := correlation.Context{
		TraceID: correlation.TraceID("123"),
		SpanID:  correlation.SpanID("456"),
	}

	want := "logs=.make/logs trace_id=123 span_id=456"
	if got := runHeaderLine(corr); got != want {
		t.Fatalf("run header = %q, want %q", got, want)
	}
}

func TestInheritedTraceparentPrintsNoHeader(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() {
		os.Args = originalArgs
	})
	os.Args = []string{"go-mk", "lint"}
	if headerless() {
		t.Fatal("lint should print a run header when it owns the trace")
	}
}

func TestLoadMakeTraceparentWalksParents(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "staticcheck")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	logs := filepath.Join(root, ".make", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	want := "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
	body := want + "\n" + strconv.Itoa(os.Getpid()) + "\n"
	if err := os.WriteFile(filepath.Join(logs, ".traceparent"), []byte(body), 0o644); err != nil {
		t.Fatalf("write traceparent: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalDir)
	})
	if err := os.Chdir(child); err != nil {
		t.Fatalf("chdir child: %v", err)
	}
	t.Setenv("MAKEFLAGS", "-j1")
	if got := loadMakeTraceparent(); got != want {
		t.Fatalf("loadMakeTraceparent = %q, want the parent tree file %q", got, want)
	}
}

func TestLoadMakeTraceparentSkipsStaleChildFile(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "staticcheck")
	if err := os.MkdirAll(filepath.Join(child, ".make", "logs"), 0o755); err != nil {
		t.Fatalf("mkdir child logs: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".make", "logs"), 0o755); err != nil {
		t.Fatalf("mkdir root logs: %v", err)
	}
	stale := "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01\n999999\n"
	if err := os.WriteFile(filepath.Join(child, ".make", "logs", ".traceparent"), []byte(stale), 0o644); err != nil {
		t.Fatalf("write child traceparent: %v", err)
	}
	want := "00-cccccccccccccccccccccccccccccccc-dddddddddddddddd-01"
	live := want + "\n" + strconv.Itoa(os.Getpid()) + "\n"
	if err := os.WriteFile(filepath.Join(root, ".make", "logs", ".traceparent"), []byte(live), 0o644); err != nil {
		t.Fatalf("write root traceparent: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalDir)
	})
	if err := os.Chdir(child); err != nil {
		t.Fatalf("chdir child: %v", err)
	}
	t.Setenv("MAKEFLAGS", "-j1")
	if got := loadMakeTraceparent(); got != want {
		t.Fatalf("loadMakeTraceparent = %q, want the parent tree file after skipping a stale child file", got)
	}
}

func TestJoinsPersistedTraceparentForResolveBin(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() {
		os.Args = originalArgs
	})
	os.Args = []string{"go-mk", "resolve-bin", "bin"}
	if !joinsPersistedTraceparent() {
		t.Fatal("resolve-bin should join a persisted TRACEPARENT")
	}
	os.Args = []string{"go-mk", "provision"}
	if joinsPersistedTraceparent() {
		t.Fatal("provision should mint a TRACEPARENT, not join a leftover file")
	}
}

func TestLoadMakeTraceparentIgnoresLeftoverFile(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".make", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	stale := "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01\n999999\n"
	if err := os.WriteFile(filepath.Join(logs, ".traceparent"), []byte(stale), 0o644); err != nil {
		t.Fatalf("write traceparent: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalDir)
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("MAKEFLAGS", "-j1")
	if got := loadMakeTraceparent(); got != "" {
		t.Fatalf("loadMakeTraceparent = %q, want empty for a leftover file from another process", got)
	}
}

func TestLoadMakeTraceparentIgnoresLegacyFile(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".make", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	legacy := "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01\n"
	if err := os.WriteFile(filepath.Join(logs, ".traceparent"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write traceparent: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalDir)
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("MAKEFLAGS", "-j1")
	if got := loadMakeTraceparent(); got != "" {
		t.Fatalf("loadMakeTraceparent = %q, want empty for a file with no owner pid", got)
	}
}
