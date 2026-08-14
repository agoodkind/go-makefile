package main

import (
	"os"
	"os/exec"
	"strings"
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

	os.Args = []string{"go-mk", "-flags"}
	if !headerless() {
		t.Fatal("-flags should not print a run header")
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

func TestPersistTraceparentWritesFile(t *testing.T) {
	dir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	persistTraceparent("00-123-456-01")
	body, err := os.ReadFile(traceparentFile)
	if err != nil {
		t.Fatalf("read %s: %v", traceparentFile, err)
	}
	if got := strings.TrimSpace(string(body)); got != "00-123-456-01" {
		t.Fatalf("traceparent file = %q, want 00-123-456-01", got)
	}
}

func TestLoadMakeTraceparentRequiresMakeflags(t *testing.T) {
	dir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	persistTraceparent("00-abc-def-01")

	originalArgs := os.Args
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	os.Args = []string{"go-mk", "lint"}
	t.Setenv("MAKEFLAGS", "")
	if got := loadMakeTraceparent(); got != "" {
		t.Fatalf("load without MAKEFLAGS = %q, want empty", got)
	}
	t.Setenv("MAKEFLAGS", "w")
	if got := loadMakeTraceparent(); got != "00-abc-def-01" {
		t.Fatalf("load with MAKEFLAGS = %q, want 00-abc-def-01", got)
	}

	os.Args = []string{"go-mk", "provision"}
	t.Setenv("MAKEFLAGS", "")
	if got := loadMakeTraceparent(); got != "00-abc-def-01" {
		t.Fatalf("provision load without MAKEFLAGS = %q, want 00-abc-def-01", got)
	}
}

func TestInheritedTraceparentPrintsNoHeader(t *testing.T) {
	binaryPath := buildGoMkForTest(t)
	dir := t.TempDir()
	traceparent := "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"

	joined := exec.Command(binaryPath)
	joined.Dir = dir
	joined.Env = testProcessEnvironment(map[string]string{"TRACEPARENT": traceparent})
	joinedOut, _ := joined.CombinedOutput()
	if count := countRunHeaders(string(joinedOut)); count != 0 {
		t.Fatalf("joined process header count = %d, want 0\n%s", count, joinedOut)
	}

	fresh := exec.Command(binaryPath)
	fresh.Dir = dir
	fresh.Env = testProcessEnvironment(nil)
	freshOut, _ := fresh.CombinedOutput()
	if count := countRunHeaders(string(freshOut)); count != 1 {
		t.Fatalf("fresh process header count = %d, want 1\n%s", count, freshOut)
	}
}

func countRunHeaders(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "logs=.make/logs") {
			count++
		}
	}
	return count
}
