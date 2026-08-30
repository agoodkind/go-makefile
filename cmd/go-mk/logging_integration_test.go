package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandLogsWithoutTraceData(t *testing.T) {
	dir := t.TempDir()
	command := exec.Command(builtTestEngine(t), "version")
	command.Dir = dir
	command.Env = testProcessEnvironment(map[string]string{"GO_MK_LOG": "debug"})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("version: %v\n%s", err, output)
	}
	assertNoTraceData(t, string(output))

	records := readGoMkLogRecords(t, dir)
	if records == "" {
		t.Fatal("go-mk wrote no structured log records")
	}
	assertNoTraceData(t, records)
}

func TestCapabilityProbeIgnoresDebugLogging(t *testing.T) {
	for _, argument := range []string{"-flags", "--flags"} {
		t.Run(argument, func(t *testing.T) {
			dir := t.TempDir()
			command := exec.Command(builtTestEngine(t), argument)
			command.Dir = dir
			command.Env = testProcessEnvironment(nil)
			plainOutput, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("plain capability probe: %v\n%s", err, plainOutput)
			}

			command = exec.Command(builtTestEngine(t), argument)
			command.Dir = dir
			command.Env = testProcessEnvironment(map[string]string{"GO_MK_LOG": "debug"})
			debugOutput, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("debug capability probe: %v\n%s", err, debugOutput)
			}
			if string(debugOutput) != string(plainOutput) {
				t.Fatalf("debug capability output changed\nplain:\n%s\ndebug:\n%s", plainOutput, debugOutput)
			}
		})
	}
}

func TestCommandIgnoresLegacyTraceFiles(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, logDir)
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	legacyFiles := map[string]string{
		".run":         "run bytes\n",
		".traceparent": "traceparent bytes\n",
	}
	for name, contents := range legacyFiles {
		path := filepath.Join(logsDir, name)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	command := exec.Command(builtTestEngine(t), "version")
	command.Dir = dir
	command.Env = testProcessEnvironment(map[string]string{"GO_MK_LOG": "debug"})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("version: %v\n%s", err, output)
	}
	assertNoTraceData(t, string(output))

	for name, want := range legacyFiles {
		path := filepath.Join(logsDir, name)
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestHelperProvisioningLogsWithoutTraceData(t *testing.T) {
	files := engineAssets(t)
	server := newFetchServer(t, files)
	dir := newConsumer(t)
	writeMakefile(t, dir, `BINARY := probe
CMD := ./cmd/probe
include bootstrap.mk
trace: go-mk-bin
	@"$(__GO_MK_ENGINE)" version
`)

	output, code := runMakeTarget(t, "make", dir, "trace", map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GO_MK_BIN":           filepath.Join(dir, ".make", "go-mk"),
		"GO_MK_LOG":           "debug",
		"HOME":                t.TempDir(),
	})
	if code != 0 {
		t.Fatalf("helper provisioning exit = %d, want 0: %s", code, output)
	}
	assertNoTraceData(t, output)
	records := readGoMkLogRecords(t, dir)
	if records == "" {
		t.Fatal("helper-provisioned go-mk wrote no structured log records")
	}
	assertNoTraceData(t, records)
}

func TestFindTraceDataRejectsEveryForbiddenForm(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "bare trace ID", text: "trace_id=abc"},
		{name: "bare span ID", text: "span_id=def"},
		{name: "uppercase traceparent", text: "TRACEPARENT=00-abc-def-01"},
		{name: "lowercase traceparent", text: "traceparent=00-abc-def-01"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if findTraceData(test.text) == "" {
				t.Fatalf("trace data in %q was not rejected", test.text)
			}
		})
	}
}

func assertNoTraceData(t *testing.T, text string) {
	t.Helper()
	if value := findTraceData(text); value != "" {
		t.Fatalf("trace data %q remains in %q", value, text)
	}
}

func findTraceData(text string) string {
	lowercaseText := strings.ToLower(text)
	for _, value := range []string{"logs=.make/logs trace_id=", "trace_id", "span_id", "traceparent"} {
		if strings.Contains(lowercaseText, value) {
			return value
		}
	}
	return ""
}

func readGoMkLogRecords(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, logDir))
	if err != nil {
		t.Fatalf("read go-mk logs: %v", err)
	}
	var records strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, logDir, entry.Name())
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read go-mk log %s: %v", entry.Name(), readErr)
		}
		records.Write(body)
	}
	return records.String()
}

func engineAssets(t *testing.T) map[string]string {
	t.Helper()
	root := repoRootForTest(t)
	assets := make(map[string]string)
	for _, name := range []string{
		"go.mk",
		"golangci.yml",
		"notices.txt",
		"scripts/go-mk-bootstrap.sh",
		"scripts/go-mk-bin.sh",
		"scripts/go-mk-fetch-one.sh",
		"scripts/go-mk-sync.sh",
	} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read engine asset %s: %v", name, err)
		}
		assets[name] = string(body)
	}
	return assets
}

func writeMakefile(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(body), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
}

func runMakeTarget(t *testing.T, makeCommand string, dir string, target string, env map[string]string) (string, int) {
	t.Helper()
	command := exec.Command(makeCommand, target)
	command.Dir = dir
	command.Env = testProcessEnvironment(env)
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	var exitErr *exec.ExitError
	if asExitError(err, &exitErr) {
		return string(output), exitErr.ExitCode()
	}
	t.Fatalf("run %s %s: %v", makeCommand, target, err)
	return "", 0
}
