# Remove go-mk tracing implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove every trace header, identifier, OpenTelemetry path, process session, state file, dependency, and trace-specific test while preserving ordinary structured logging.

**Architecture:** `setupLogging` installs the existing summary handler and per-concern JSONL router directly as the default `slog` handler. Command dispatch no longer creates, propagates, stores, or renders trace data. Static analysis stops enforcing trace context.

**Tech Stack:** Go, `log/slog`, `goodkind.io/gklog`, GNU Make, Go tests, consumer-check.

**Spec:** `docs/superpowers/specs/2026-08-29-remove-go-mk-tracing-design.md`

## Global constraints

- Keep per-concern JSONL logs and every `GO_MK_LOG` mode.
- Remove `trace_id`, `span_id`, `TRACEPARENT`, OpenTelemetry, process inspection, sessions, locks, cache state, and legacy trace handling.
- Do not change consumer repositories or committed bootstrap files.
- Ignore old `.make/logs/.run` and `.make/logs/.traceparent` files without changing them.
- Preserve command output and exit status apart from removed trace data.

---

### Task 1: Lock the trace-free command contract

**Files:**
- Replace: `cmd/go-mk/logging_integration_test.go`
- Delete: `cmd/go-mk/logging_test.go`

**Interfaces:**
- Consumes: `builtTestEngine`, `testProcessEnvironment`, `newConsumer`, and helper fixtures.
- Produces: public-boundary tests that reject trace output and fields while requiring ordinary JSONL logs.

- [ ] **Step 1: Replace trace session tests with a failing command test**

Use the real engine and assert both visible output and stored records:

```go
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

func assertNoTraceData(t *testing.T, text string) {
    t.Helper()
    forbidden := []string{"logs=.make/logs trace_id=", `"trace_id"`, `"span_id"`, "traceparent"}
    for _, value := range forbidden {
        if strings.Contains(text, value) {
            t.Fatalf("trace data %q remains in %q", value, text)
        }
    }
}
```

Retain one helper-provisioning Make fixture. Require exit zero and pass its combined output through `assertNoTraceData`.

- [ ] **Step 2: Add a failing legacy-file preservation test**

Create `.make/logs/.run` and `.make/logs/.traceparent` with known bytes. Run `go-mk version`. Read both files and require exact byte equality after the command.

- [ ] **Step 3: Prove the old implementation fails**

Run:

```bash
go test ./cmd/go-mk -run 'TestCommandLogsWithoutTraceData|TestCommandIgnoresLegacyTraceFiles|TestHelperProvisioningLogsWithoutTraceData' -count=1
```

Expected: the command test fails on the current trace header and trace fields.

### Task 2: Remove runtime tracing and session state

**Files:**
- Modify: `cmd/go-mk/logging.go`
- Modify: `cmd/go-mk/main.go`
- Delete: `cmd/go-mk/logging_session.go`
- Delete: `cmd/go-mk/logging_session_test.go`

**Interfaces:**
- Consumes: `logsummary.New`, `gklog.NewRouter`, and `slog.Handler`.
- Produces: `setupLogging()` with no return value and no trace side effects.

- [ ] **Step 1: Replace tracing setup with direct structured logging**

Use this implementation:

```go
func setupLogging() {
    mode := logsummary.ParseMode(os.Getenv("GO_MK_LOG"))
    level := slog.LevelInfo
    if mode == logsummary.ModeDebug {
        level = slog.LevelDebug
    }
    summary := logsummary.New(os.Stderr, mode)
    router := gklog.NewRouter(logDir, level, summary, gklog.RouterOptions{
        FallbackConcern: "go-mk",
        Rotation:        gklog.RotationConfig{},
    })
    slog.SetDefault(slog.New(router))
}
```

Delete all correlation imports, constants, command classification, trace setup, propagation, session claims, and header rendering from `logging.go`.

- [ ] **Step 2: Simplify the process entrypoint**

Use this entrypoint and delete `capabilityProbe`:

```go
func main() {
    setupLogging()
    slog.Debug("go-mk invoked")
    os.Exit(run())
}
```

- [ ] **Step 3: Delete session production and unit files**

Delete `logging_session.go` and `logging_session_test.go`. Add no migration or cleanup replacement.

- [ ] **Step 4: Format and prove the focused tests pass**

Run `gofmt` on the modified Go files. Then run the focused command from Task 1 and require PASS.

- [ ] **Step 5: Commit the runtime removal**

```bash
git add cmd/go-mk
git commit -S -m "Remove go-mk runtime tracing" -m "Co-authored-by: Codex <noreply@openai.com>"
```

### Task 3: Remove trace enforcement and dependencies

**Files:**
- Modify: `staticcheck/analyzers.go`
- Modify: `staticcheck/structural.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: the remaining structured logging analyzers and `goodkind.io/gklog`.
- Produces: an analyzer set with no trace-context rule and an engine dependency graph without OpenTelemetry.

- [ ] **Step 1: Remove the trace analyzer**

Remove `SlogMissingTraceIDAnalyzer` from `Analyzers()`.

Delete `SlogMissingTraceIDAnalyzer`, `runSlogMissingTraceID`, `checkSlogMissingTraceIDInFile`, `reportSlogCallsWithoutTraceID`, `slogCallHasTraceContext`, and `contextParamName`.

Keep `isAnyLevelSlogCall`. The gRPC peer-enrichment analyzer uses it.

- [ ] **Step 2: Remove unused dependencies**

Run `go mod tidy` from the repository root. Keep `goodkind.io/gklog`. Require direct OpenTelemetry requirements and unused sums to disappear.

- [ ] **Step 3: Verify the dependency graph**

Run `go list -deps ./cmd/go-mk`. Require no package beginning with `go.opentelemetry.io/`.

- [ ] **Step 4: Test both Go modules**

Run `go test ./...` from the root. Run `go test ./... -count=1` from `staticcheck/`. Require both suites to pass.

- [ ] **Step 5: Commit enforcement and dependency removal**

```bash
git add staticcheck/analyzers.go staticcheck/structural.go go.mod go.sum
git commit -S -m "Remove trace enforcement and dependencies" -m "Co-authored-by: Codex <noreply@openai.com>"
```

### Task 4: Prove complete removal

**Files:**
- Modify only if verification exposes remaining trace machinery.

**Interfaces:**
- Consumes: the trace-free engine from Tasks 2 and 3.
- Produces: local, race, consumer, and pull request evidence for every acceptance criterion.

- [ ] **Step 1: Audit tracked source and dependencies**

Use semantic search for `TRACEPARENT`, `trace_id`, `span_id`, `traceparent`, `OpenTelemetry`, `opentelemetry`, `SlogMissingTraceID`, session paths, process ancestry, and trace lock names.

Expected: no production or test hit remains. The approved spec and plan may retain historical names.

- [ ] **Step 2: Run full local verification**

Run `go test ./cmd/go-mk -count=1`, `go test -race ./cmd/go-mk -count=1`, `make test`, and `make check`. Require every command to exit zero.

- [ ] **Step 3: Validate every surveyed consumer entry point**

Run `cmd/consumer-check` in place against `origin/main` and `HEAD`. Cover helper provisioning, direct fetch, inline fetch, nested Make, recursive Make, and packages below repository roots.

Expected: each consumer keeps its existing exit status. Head output contains no trace header, `trace_id`, or `span_id`.

- [ ] **Step 4: Verify signatures and update pull request 142**

Fetch origin. Verify every commit in `origin/main..HEAD` with `git verify-commit` and a raw `gpgsig` inspection. Push `one-trace-per-make`, update the pull request title and description for complete tracing removal, and monitor required checks and review threads.

Expected: remote head equals local `HEAD`, required checks pass, and no unresolved review thread applies to the removed design.
