package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const testProgressInterval = 30 * time.Second

type testAction string

const (
	testStart       testAction = "start"
	testRun         testAction = "run"
	testPause       testAction = "pause"
	testContinue    testAction = "cont"
	testPass        testAction = "pass"
	testFail        testAction = "fail"
	testSkip        testAction = "skip"
	testOutput      testAction = "output"
	testBenchmark   testAction = "bench"
	testBuildStart  testAction = "build-start"
	testBuildOutput testAction = "build-output"
	testBuildFail   testAction = "build-fail"
	testAttribute   testAction = "attr"
	testArtifacts   testAction = "artifacts"
)

type testEvent struct {
	Time       time.Time  `json:"Time"`
	Action     testAction `json:"Action"`
	Package    string     `json:"Package"`
	Test       string     `json:"Test"`
	Elapsed    float64    `json:"Elapsed"`
	Output     string     `json:"Output"`
	OutputType string     `json:"OutputType"`
	ImportPath string     `json:"ImportPath"`
	Key        string     `json:"Key"`
	Value      string     `json:"Value"`
	Path       string     `json:"Path"`
}

type testIdentity struct {
	packagePath string
	testName    string
}

func (identity testIdentity) label() string {
	if identity.testName == "" {
		return "Package " + identity.packagePath
	}
	return "Test " + identity.packagePath + "/" + identity.testName
}

type testProgress struct {
	started time.Time
	active  map[testIdentity]time.Time
	paused  map[testIdentity]time.Time
	cached  map[string]bool
	failed  bool
}

// runGoTest preserves the lint environment and child stderr while rendering
// stdout events as they arrive, including events from concurrent packages.
func runGoTest(args []string) (err error) {
	slog.Info("lint run go tests", slog.Int("args", len(args)))
	defer func() {
		if err != nil {
			slog.Error("lint go tests failed", slog.String("err", err.Error()))
		}
	}()
	command := exec.CommandContext(context.Background(), "go", args...)
	command.Env = lintEnv()
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open go test output: %w", err)
	}
	defer func() { _ = stdout.Close() }()
	writeStdout("Running go test with live progress (updates every 30s).\n")
	if err := command.Start(); err != nil {
		return fmt.Errorf("start go test: %w", err)
	}
	reportErr := renderTestProgress(stdout, testProgressInterval)
	return errors.Join(command.Wait(), reportErr)
}

type testOutputLine struct {
	text string
	err  error
}

// readTestOutput reads complete JSON records without limiting diagnostic size.
func readTestOutput(reader io.Reader, lines chan<- testOutputLine) {
	buffer := bufio.NewReader(reader)
	for {
		line, err := buffer.ReadString('\n')
		if len(line) > 0 {
			lines <- testOutputLine{text: line}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				lines <- testOutputLine{err: err}
			}
			return
		}
	}
}

func renderTestProgress(reader io.Reader, interval time.Duration) error {
	progress := testProgress{
		started: time.Now(),
		active:  make(map[testIdentity]time.Time),
		paused:  make(map[testIdentity]time.Time),
		cached:  make(map[string]bool),
	}
	lines := make(chan testOutputLine)
	go func() {
		defer close(lines)
		defer func() {
			if recovered := recover(); recovered != nil {
				lines <- testOutputLine{err: fmt.Errorf("go test output reader panicked: %v", recovered)}
			}
		}()
		readTestOutput(reader, lines)
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var reportErr error
	for {
		select {
		case line, open := <-lines:
			if !open {
				err := errors.Join(reportErr, progress.finish())
				if err != nil {
					slog.Error("lint go test progress failed", slog.String("err", err.Error()))
				}
				return err
			}
			if line.err != nil {
				reportErr = errors.Join(reportErr, fmt.Errorf("read go test events: %w", line.err))
				continue
			}
			var event testEvent
			if err := json.Unmarshal([]byte(line.text), &event); err != nil {
				writeStdout(line.text)
				reportErr = errors.Join(reportErr, fmt.Errorf("decode go test event: %w", err))
				continue
			}
			reportErr = errors.Join(reportErr, progress.event(event))
		case now := <-ticker.C:
			progress.heartbeat(now)
		}
	}
}

func (progress *testProgress) event(event testEvent) error {
	identity := testIdentity{packagePath: event.Package, testName: event.Test}
	switch event.Action {
	case testStart, testRun, testContinue:
		started := event.Time
		if started.IsZero() {
			started = time.Now()
		}
		if pausedAt, ok := progress.paused[identity]; ok {
			started = pausedAt
			delete(progress.paused, identity)
		}
		progress.active[identity] = started
		if event.Action == testContinue {
			writeStdout(identity.label() + " continued.\n")
		} else {
			writeStdout(identity.label() + " started.\n")
		}
	case testPause:
		progress.paused[identity] = progress.active[identity]
		delete(progress.active, identity)
		writeStdout(identity.label() + " paused.\n")
	case testPass, testFail, testSkip, testBenchmark:
		delete(progress.active, identity)
		delete(progress.paused, identity)
		if event.Test == "" {
			progress.finishBenchmarks(event.Package)
		}
		if event.Action == testFail {
			progress.failed = true
		}
		if event.Test == "" && progress.cached[event.Package] {
			writeStdout(fmt.Sprintf("%s: %s (cached).\n", identity.label(), event.Action))
		} else {
			writeStdout(fmt.Sprintf("%s: %s (%.3fs).\n", identity.label(), event.Action, event.Elapsed))
		}
	case testOutput:
		if event.Test == "" && strings.Contains(event.Output, "\t(cached)") {
			progress.cached[event.Package] = true
		}
		if !testFramingOutput(event) {
			writeStdout(event.Output)
		}
	case testBuildStart:
		writeStdout("Building " + event.ImportPath + ".\n")
	case testBuildOutput:
		writeStdout(event.Output)
	case testBuildFail:
		progress.failed = true
		writeStdout("Build failed for " + event.ImportPath + ".\n")
	case testAttribute:
		writeStdout(identity.label() + " attribute: " + event.Key + "=" + event.Value + ".\n")
	case testArtifacts:
		writeStdout(identity.label() + " artifacts: " + event.Path + ".\n")
	default:
		return fmt.Errorf("go test emitted unknown action %q", event.Action)
	}
	return nil
}

// finishBenchmarks removes benchmark runs when their package finishes. Go can
// omit individual benchmark completion events after printing their results.
func (progress *testProgress) finishBenchmarks(packagePath string) {
	for identity := range progress.active {
		if identity.packagePath == packagePath && strings.HasPrefix(identity.testName, "Benchmark") {
			delete(progress.active, identity)
		}
	}
}

// testFramingOutput suppresses only output that Go explicitly marks as framing.
func testFramingOutput(event testEvent) bool {
	return event.OutputType == "frame"
}

func (progress *testProgress) heartbeat(now time.Time) {
	writeStdout("Go tests are still running (elapsed " + now.Sub(progress.started).Round(time.Second).String() + ").\n")
	for _, identity := range sortedTestIdentities(progress.active) {
		elapsed := now.Sub(progress.active[identity]).Round(time.Second)
		writeStdout(identity.label() + " is still running (elapsed " + elapsed.String() + ").\n")
	}
}

func (progress *testProgress) finish() error {
	if progress.failed {
		return errors.New("go test reported a test or build failure")
	}
	unfinished := maps.Clone(progress.active)
	maps.Copy(unfinished, progress.paused)
	identities := sortedTestIdentities(unfinished)
	if len(identities) > 0 {
		labels := make([]string, 0, len(identities))
		for _, identity := range identities {
			labels = append(labels, identity.label())
		}
		return fmt.Errorf("go test ended before reporting results for %s", strings.Join(labels, ", "))
	}
	return nil
}

func sortedTestIdentities(active map[testIdentity]time.Time) []testIdentity {
	identities := make([]testIdentity, 0, len(active))
	for identity := range active {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(first, second int) bool {
		return identities[first].label() < identities[second].label()
	})
	return identities
}
