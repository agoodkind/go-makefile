package main_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGoTestLiveProgress(t *testing.T) {
	engine := noticeGitBuildEngine(t, os.Environ())
	for _, entry := range []string{"command", "make"} {
		t.Run(entry, func(t *testing.T) {
			dir := newTestProgressConsumer(t)
			started := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseTests := func() { releaseOnce.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				close(started)
				select {
				case <-release:
					writer.WriteHeader(http.StatusOK)
				case <-request.Context().Done():
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(releaseTests)
			targets := "-count=1 -timeout=90s -artifacts -outputdir=" + dir + " -run=TestLive|TestAfter|TestSkip|TestOther ./..."
			if entry == "make" {
				targets = "-json -v " + targets
			}
			command := testProgressCommand(t, engine, dir, entry, map[string]string{
				"GO_TEST_TARGETS":         targets,
				"GO_TEST_LDFLAGS":         "-X 'example.com/progress.stampedVersion=release candidate'",
				"GO_MK_PROGRESS_GATE_URL": server.URL,
			})
			output := newObservedTestOutput()
			command.Stdout = output
			command.Stderr = output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- command.Wait() }()
			select {
			case <-started:
			case err := <-finished:
				t.Fatalf("test command finished before requesting release: %v\n%s", err, output.text())
			case <-time.After(40 * time.Second):
				t.Fatalf("test did not request its release: %s", output.text())
			}
			output.require(t, "Test example.com/progress/TestLive/active started.", 5*time.Second)
			output.require(t, "child stderr diagnostic", 5*time.Second)
			if entry == "command" {
				output.require(t, "Test example.com/progress/TestLive/active is still running (elapsed ", 35*time.Second)
				if strings.Contains(output.text(), "Test example.com/progress/TestLive/parallel is still running") {
					t.Fatalf("heartbeat included a paused test: %s", output.text())
				}
			}
			select {
			case err := <-finished:
				t.Fatalf("command finished before the test release: %v\n%s", err, output.text())
			default:
			}
			releaseTests()
			if err := <-finished; err != nil {
				t.Fatalf("test command: %v\n%s", err, output.text())
			}
			for _, expected := range []string{
				"Running go test with live progress",
				"Test example.com/progress/TestLive/parallel: pass (",
				"Test example.com/progress/TestAfter: pass (",
				"Test example.com/progress/TestSkip: skip (",
				"Package example.com/progress: pass (",
				"Package example.com/progress/other: pass (",
				"Package example.com/progress/empty: skip (",
				"skip diagnostic", "stamp: release candidate",
				"Test example.com/progress/TestAfter attribute: kind=progress fixture.",
				"Test example.com/progress/TestAfter artifacts: ",
				"=== RUN   ordinary stdout diagnostic",
				"--- FAIL: ordinary stderr diagnostic",
				"\nPASS\nFAIL\n",
			} {
				output.require(t, expected, time.Second)
			}
			for _, duplicate := range []string{"=== RUN   TestLive", "=== PAUSE TestLive", "=== CONT  TestLive", "--- PASS:", "--- SKIP:"} {
				if strings.Contains(output.text(), duplicate) {
					t.Errorf("report duplicated test framing %q: %s", duplicate, output.text())
				}
			}
		})
	}
}

func TestGoTestFailureDiagnostics(t *testing.T) {
	engine := noticeGitBuildEngine(t, os.Environ())
	for _, scenario := range []struct {
		name       string
		targets    string
		source     string
		goflags    string
		diagnostic string
	}{
		{name: "test failure", targets: "-count=1 -run=TestFailure .", diagnostic: "intentional test failure diagnostic"},
		{name: "build failure", targets: "./...", source: "package progress\nvar broken = missingIdentifier\n", diagnostic: "undefined: missingIdentifier"},
		{name: "incomplete tests", targets: "-count=1 -run=TestExit . -args -test.paniconexit0=false", diagnostic: "go test ended before reporting results for "},
		{name: "disabled JSON", targets: "-json=false -count=1 -run=TestAfter .", diagnostic: "decode go test event"},
		{name: "disabled verbosity", targets: "-v=false -run=TestAfter .", diagnostic: "GO_TEST_TARGETS cannot disable verbose output during live test reporting"},
		{name: "GOFLAGS disabled verbosity", targets: "-run=TestAfter .", goflags: "-v=false", diagnostic: "GOFLAGS cannot disable verbose output during live test reporting"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := newTestProgressConsumer(t)
			if scenario.source != "" {
				if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte(scenario.source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			command := testProgressCommand(t, engine, dir, "command", map[string]string{
				"GO_TEST_TARGETS": scenario.targets,
				"GOFLAGS":         scenario.goflags,
			})
			output, err := command.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("command error = %v, want exit 1\n%s", err, output)
			}
			if !strings.Contains(string(output), scenario.diagnostic) {
				t.Fatalf("missing diagnostic %q: %s", scenario.diagnostic, output)
			}
			if scenario.name == "test failure" && !strings.Contains(string(output), "Test example.com/progress/TestFailure: fail (") {
				t.Fatalf("missing named test failure: %s", output)
			}
		})
	}
}

func TestGoTestCachedPackage(t *testing.T) {
	engine := noticeGitBuildEngine(t, os.Environ())
	dir := newTestProgressConsumer(t)
	for attempt := range 2 {
		command := testProgressCommand(t, engine, dir, "command", map[string]string{"GO_TEST_TARGETS": "-run=TestAfter ."})
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("test command: %v\n%s", err, output)
		}
		if attempt == 1 && !strings.Contains(string(output), "Package example.com/progress: pass (cached).") {
			t.Fatalf("missing cached package status: %s", output)
		}
	}
}

func TestGoTestBenchmarks(t *testing.T) {
	engine := noticeGitBuildEngine(t, os.Environ())
	dir := newTestProgressConsumer(t)
	command := testProgressCommand(t, engine, dir, "command", map[string]string{
		"GO_TEST_TARGETS": "-run=^$ -bench=^BenchmarkProgress$ -benchtime=1x .",
	})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("benchmark command: %v\n%s", err, output)
	}
	for _, expected := range []string{"BenchmarkProgress", "ns/op", "Package example.com/progress: pass ("} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("missing benchmark result %q: %s", expected, output)
		}
	}
}

func TestGoTestVerboseFlags(t *testing.T) {
	engine := noticeGitBuildEngine(t, os.Environ())
	for _, scenario := range []struct {
		name    string
		targets string
		goflags string
	}{
		{name: "GOFLAGS", targets: "-run=TestAfter .", goflags: "-v"},
		{name: "verbose alias", targets: "-test.v=true -run=TestAfter ."},
		{name: "test arguments", targets: "-run=TestAfter . -args -test.v=true"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := newTestProgressConsumer(t)
			command := testProgressCommand(t, engine, dir, "command", map[string]string{
				"GO_TEST_TARGETS": scenario.targets,
				"GOFLAGS":         scenario.goflags,
			})
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("test command: %v\n%s", err, output)
			}
			for _, expected := range []string{"=== RUN   ordinary stdout diagnostic", "--- FAIL: ordinary stderr diagnostic", "Test example.com/progress/TestAfter: pass ("} {
				if !strings.Contains(string(output), expected) {
					t.Fatalf("missing test output %q: %s", expected, output)
				}
			}
		})
	}
}

func newTestProgressConsumer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(testProgressRepoRoot(t), "cmd", "go-mk", "testdata", "testprogress")
	if err := os.CopyFS(dir, os.DirFS(root)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testProgressCommand(t *testing.T, engine, dir, entry string, environment map[string]string) *exec.Cmd {
	t.Helper()
	commandContext, cancel := context.WithTimeout(t.Context(), 100*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(commandContext, engine, "test")
	if entry == "make" {
		command = exec.CommandContext(commandContext, "make", "test")
	}
	environment["GO_MK_DEV_DIR"] = testProgressRepoRoot(t)
	environment["GO_MK_BIN"] = engine
	environment["GOWORK"] = "off"
	for _, variable := range []string{"PATH", "HOME", "TMPDIR"} {
		environment[variable] = os.Getenv(variable)
	}
	command.Dir = dir
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	return command
}

func testProgressRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

type observedTestOutput struct {
	mutex   sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

func newObservedTestOutput() *observedTestOutput {
	return &observedTestOutput{changed: make(chan struct{}, 1)}
}

func (output *observedTestOutput) Write(contents []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	count, err := output.buffer.Write(contents)
	select {
	case output.changed <- struct{}{}:
	default:
	}
	return count, err
}

func (output *observedTestOutput) text() string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.buffer.String()
}

func (output *observedTestOutput) require(t *testing.T, expected string, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for !strings.Contains(output.text(), expected) {
		select {
		case <-output.changed:
		case <-deadline.C:
			t.Fatalf("missing live output %q:\n%s", expected, output.text())
		}
	}
}
