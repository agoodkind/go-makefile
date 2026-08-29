package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

var traceHeaderPattern = regexp.MustCompile(`(?m)^logs=\.make/logs trace_id=([[:xdigit:]]+) span_id=([[:xdigit:]]+)$`)
var traceRecordPattern = regexp.MustCompile(`"trace_id":"([[:xdigit:]]+)"`)

func TestTraceSessionHelperProvisioning(t *testing.T) {
	files := traceEngineAssets(t)
	server := newFetchServer(t, files)

	for _, makeCommand := range installedMakeCommands(t) {
		t.Run(filepath.Base(makeCommand), func(t *testing.T) {
			dir := newConsumer(t)
			writeTraceMakefile(t, dir, `BINARY := probe
CMD := ./cmd/probe
include bootstrap.mk
trace: go-mk-bin
	@"$(__GO_MK_ENGINE)" version
`)
			cacheDir := t.TempDir()
			output, code := runTraceMake(t, makeCommand, dir, map[string]string{
				"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
				"GO_MK_BIN":           filepath.Join(dir, ".make", "go-mk"),
				"HOME":                cacheDir,
			})
			if code != 0 {
				t.Fatalf("helper trace exit = %d, want 0: %s", code, output)
			}
			traceID := assertOneTraceHeader(t, output)
			assertTraceLogRecords(t, dir, traceID)
			assertNoNewLegacyTraceFiles(t, dir)
		})
	}
}

func TestTraceSessionInlineFetch(t *testing.T) {
	files := traceEngineAssets(t)
	server := newFetchServer(t, files)
	dir := t.TempDir()
	seedTestEngine(t, dir)
	bootstrap := `GO_MK := .make/go.mk
GO_MK_API_REPO ?= agoodkind/go-makefile
GO_MK_API_REF ?= main
GO_MK_BOOTSTRAP_FETCHED := 1
$(shell mkdir -p .make && curl -sS -o .make/snapshot.tar.gz "$(GO_MK_CODELOAD_BASE)/$(GO_MK_API_REPO)/tar.gz/$(GO_MK_API_REF)" && tar -xzf .make/snapshot.tar.gz -C .make --strip-components 1)
-include $(GO_MK)
`
	if err := os.WriteFile(filepath.Join(dir, "bootstrap.mk"), []byte(bootstrap), 0o644); err != nil {
		t.Fatalf("write inline bootstrap: %v", err)
	}
	writeTraceMakefile(t, dir, `BINARY := probe
include bootstrap.mk
trace: go-mk-bin
	@"$(__GO_MK_ENGINE)" version
`)

	output, code := runTraceMake(t, "make", dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GO_MK_BIN":           filepath.Join(dir, ".make", "go-mk"),
		"HOME":                t.TempDir(),
	})
	if code != 0 {
		t.Fatalf("inline trace exit = %d, want 0: %s", code, output)
	}
	assertOneTraceHeader(t, output)
	assertNoNewLegacyTraceFiles(t, dir)
}

func TestTraceSessionDirectFetch(t *testing.T) {
	dir := t.TempDir()
	seedTestEngine(t, dir)
	assets := traceEngineAssets(t)
	server, requests := newRawAssetServer(t, assets)
	if err := os.WriteFile(filepath.Join(dir, ".make", "go.mk"), []byte(assets["go.mk"]), 0o644); err != nil {
		t.Fatalf("seed direct go.mk: %v", err)
	}
	writeTraceMakefile(t, dir, `BINARY := probe
GO_MK_PROVISION := 1
GO_MK_BASE_URL := `+server.URL+`
include .make/go.mk
trace: go-mk-bin
	@"$(__GO_MK_ENGINE)" version
`)

	output, code := runTraceMake(t, "make", dir, map[string]string{
		"GO_MK_BIN": filepath.Join(dir, ".make", "go-mk"),
		"HOME":      t.TempDir(),
	})
	if code != 0 {
		t.Fatalf("direct trace exit = %d, want 0: %s", code, output)
	}
	assertOneTraceHeader(t, output)
	if len(requests()) == 0 {
		t.Fatal("direct fetch made no raw asset requests")
	}
}

func TestTraceSessionNestedAndRecursiveMake(t *testing.T) {
	dir := t.TempDir()
	seedTestEngine(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "Makefile"), []byte(`trace:
	@"../.make/go-mk" version
`), 0o644); err != nil {
		t.Fatalf("write nested Makefile: %v", err)
	}
	writeTraceMakefile(t, dir, `trace-recursive:
	@$(MAKE) trace-child
trace-child:
	@".make/go-mk" version
trace-nested:
	@$(MAKE) -C nested trace
`)

	for _, target := range []string{"trace-recursive", "trace-nested"} {
		t.Run(target, func(t *testing.T) {
			output, code := runNamedMake(t, "make", dir, target, map[string]string{
				"HOME": t.TempDir(),
			})
			if code != 0 {
				t.Fatalf("%s exit = %d, want 0: %s", target, code, output)
			}
			assertOneTraceHeader(t, output)
		})
	}
}

func TestTraceSessionConcurrentOuterMakes(t *testing.T) {
	dir := t.TempDir()
	seedTestEngine(t, dir)
	writeTraceMakefile(t, dir, `trace:
	@".make/go-mk" version
`)
	cacheDir := t.TempDir()
	type result struct {
		output string
		code   int
	}
	results := make(chan result, 2)
	var waitGroup sync.WaitGroup
	for i := 0; i < 2; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			output, code := runTraceMake(t, "make", dir, map[string]string{
				"HOME": cacheDir,
			})
			results <- result{output: output, code: code}
		}()
	}
	waitGroup.Wait()
	close(results)

	traceIDs := make(map[string]bool)
	for result := range results {
		if result.code != 0 {
			t.Fatalf("concurrent trace exit = %d, want 0: %s", result.code, result.output)
		}
		traceID := assertOneTraceHeader(t, result.output)
		traceIDs[traceID] = true
	}
	if len(traceIDs) != 2 {
		t.Fatalf("concurrent trace ids = %v, want two distinct outer traces", traceIDs)
	}
}

func TestTraceSessionImportsHeaderedLegacyEngineTrace(t *testing.T) {
	dir := t.TempDir()
	seedTestEngine(t, dir)
	const traceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const traceparent = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
	writeTraceMakefile(t, dir, `trace:
	@mkdir -p .make/logs; outer=$$PPID; printf '`+traceparent+`\n%s\n' "$$outer" > .make/logs/.traceparent; printf '`+traceID+`\n' > .make/logs/.run; printf 'logs=.make/logs trace_id=`+traceID+` span_id=bbbbbbbbbbbbbbbb\n'; ".make/go-mk" version
`)

	output, code := runTraceMake(t, "make", dir, map[string]string{
		"HOME": t.TempDir(),
	})
	if code != 0 {
		t.Fatalf("legacy adoption exit = %d, want 0: %s", code, output)
	}
	if got := assertOneTraceHeader(t, output); got != traceID {
		t.Fatalf("legacy adoption trace_id = %q, want %q", got, traceID)
	}
}

func TestTraceSessionCapabilityProbeCreatesNoSession(t *testing.T) {
	dir := t.TempDir()
	seedTestEngine(t, dir)
	writeTraceMakefile(t, dir, `trace:
	@".make/go-mk" -flags >/dev/null
`)
	cacheDir := t.TempDir()

	output, code := runTraceMake(t, "make", dir, map[string]string{
		"HOME": cacheDir,
	})
	if code != 0 {
		t.Fatalf("capability probe exit = %d, want 0: %s", code, output)
	}
	if matches := traceHeaderPattern.FindAllStringSubmatch(output, -1); len(matches) != 0 {
		t.Fatalf("capability probe printed %d headers, want 0: %s", len(matches), output)
	}
	entries, err := os.ReadDir(filepath.Join(cacheDir, "Library", "Caches", "go-makefile", "traces"))
	if !os.IsNotExist(err) {
		if err != nil {
			t.Fatalf("read trace session store: %v", err)
		}
		t.Fatalf("capability probe created %d trace session entries", len(entries))
	}
}

func TestTraceSessionHonorsInheritedTraceparent(t *testing.T) {
	dir := t.TempDir()
	command := exec.Command(builtTestEngine(t), "version")
	command.Dir = dir
	command.Env = testProcessEnvironment(map[string]string{
		"TRACEPARENT": "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01",
		"HOME":        t.TempDir(),
	})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run inherited trace: %v: %s", err, output)
	}
	if matches := traceHeaderPattern.FindAllStringSubmatch(string(output), -1); len(matches) != 0 {
		t.Fatalf("inherited trace printed %d headers, want 0: %s", len(matches), output)
	}
}

func traceEngineAssets(t *testing.T) map[string]string {
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

func newRawAssetServer(t *testing.T, assets map[string]string) (*httptest.Server, func() []string) {
	t.Helper()
	var mutex sync.Mutex
	requests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(request.URL.Path, "/")
		mutex.Lock()
		requests = append(requests, name)
		mutex.Unlock()
		body, ok := assets[name]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]string(nil), requests...)
	}
}

func writeTraceMakefile(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(body), 0o644); err != nil {
		t.Fatalf("write trace Makefile: %v", err)
	}
}

func installedMakeCommands(t *testing.T) []string {
	t.Helper()
	commands := []string{"make"}
	if gmake, err := exec.LookPath("gmake"); err == nil {
		commands = append(commands, gmake)
	}
	return commands
}

func runTraceMake(t *testing.T, makeCommand string, dir string, env map[string]string) (string, int) {
	t.Helper()
	return runNamedMake(t, makeCommand, dir, "trace", env)
}

func runNamedMake(t *testing.T, makeCommand string, dir string, target string, env map[string]string) (string, int) {
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

func assertOneTraceHeader(t *testing.T, output string) string {
	t.Helper()
	matches := traceHeaderPattern.FindAllStringSubmatch(output, -1)
	if len(matches) != 1 {
		t.Fatalf("trace headers = %d, want 1: %s", len(matches), output)
	}
	if matches[0][1] == "" || matches[0][2] == "" {
		t.Fatalf("trace header has an empty id: %q", matches[0])
	}
	return matches[0][1]
}

func assertNoNewLegacyTraceFiles(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{traceparentFile, runSentinel} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			if err != nil {
				t.Fatalf("stat legacy trace file %s: %v", name, err)
			}
			t.Fatalf("new engine wrote legacy trace file %s", name)
		}
	}
}

func assertTraceLogRecords(t *testing.T, dir string, traceID string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, logDir))
	if err != nil {
		t.Fatalf("read trace logs: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(dir, logDir, entry.Name()))
		if readErr != nil {
			t.Fatalf("read trace log %s: %v", entry.Name(), readErr)
		}
		for _, match := range traceRecordPattern.FindAllStringSubmatch(string(body), -1) {
			count++
			if match[1] != traceID {
				t.Fatalf("trace log %s has trace_id %q, want %q", entry.Name(), match[1], traceID)
			}
		}
	}
	if count == 0 {
		t.Fatal("trace run wrote no structured log records")
	}
}
