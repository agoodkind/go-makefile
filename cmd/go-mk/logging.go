// Logging setup for go-mk. Every run fans its structured records out to
// per-concern JSONL files under logDir and, collapsed, to the summary stream on
// stderr, with the run's trace and span ids stamped on every record. The first
// go-mk process of a run mints the trace, prints the one-line header, and
// exports the traceparent so the gate sub-makes it spawns join the same trace
// and stay quiet. Auxiliary subcommands that run as make prerequisites, such as
// notice, are not a run and never print the header.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"goodkind.io/gklog"
	"goodkind.io/gklog/correlation"
	"goodkind.io/gklog/trace"
	"goodkind.io/go-makefile/internal/logsummary"
)

// logDir is the per-concern JSONL directory every run writes under.
const logDir = ".make/logs"

// runSentinel records the trace id whose header has already been printed, so the
// header prints once per run even though a run spans several go-mk processes (a
// build-check process and its gate sub-makes all share one trace through the
// inherited traceparent).
const runSentinel = ".make/logs/.run"

// traceparentFile is the W3C TRACEPARENT the first go-mk of a make parse mints.
// Later recipe processes load it when MAKEFLAGS is set, matching os.Setenv
// for in-process children.
const traceparentFile = ".make/logs/.traceparent"

// headerlessCommands are auxiliary subcommands that run as make prerequisites
// and are not user-facing runs, so they do not print the run header.
var headerlessCommands = map[string]bool{
	"cache-manifest": true,
	"notice":         true,
}

// headerless reports whether this invocation is an auxiliary subcommand that
// should not print the run header. It reads the first non-flag argument as the
// subcommand name.
func headerless() bool {
	for _, arg := range os.Args[1:] {
		if arg == "-flags" || arg == "--flags" {
			return true
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return headerlessCommands[arg]
	}
	return false
}

// setupLogging installs the run's structured logger and returns a cleanup to run
// at process exit. It seeds an OpenTelemetry span from an inherited traceparent
// (so the whole run shares one trace), derives the correlation ids from that
// span, stamps them on every record, and prints the one-line header when this
// process owns the run.
func setupLogging() func() {
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

	closer, _ := trace.Setup(trace.Options{
		ServiceName: "go-mk",
		Endpoint:    os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})

	inherited := os.Getenv("TRACEPARENT")
	if inherited == "" {
		inherited = loadMakeTraceparent()
		if inherited != "" {
			_ = os.Setenv("TRACEPARENT", inherited)
		}
	}
	ctx := context.Background()
	if inherited != "" {
		ctx = otel.GetTextMapPropagator().Extract(
			ctx, propagation.MapCarrier{"traceparent": inherited})
	}
	ctx, span := trace.StartSpan(ctx, "go-mk")

	corr := correlation.Context{
		TraceID: correlation.TraceID(trace.IDFromContext(ctx)),
		SpanID:  correlation.SpanID(trace.SpanIDFromContext(ctx)),
	}
	// A direct go-mk run (no inherited traceparent) owns the trace and exports it
	// so any child process it spawns, such as a gate sub-make, joins the same
	// trace. A make-driven run already has the traceparent in its environment.
	if inherited == "" {
		if traceparent := corr.Traceparent(); traceparent != "" {
			_ = os.Setenv("TRACEPARENT", traceparent)
			persistTraceparent(traceparent)
		}
	}

	handler := correlation.SlogHandler(router, correlation.HandlerOptions{
		Required: []string{"trace_id", "span_id"},
	})
	slog.SetDefault(slog.New(handler.WithAttrs(corr.Attrs())))

	if inherited == "" && !headerless() {
		printHeaderOnce(corr)
	}

	return func() {
		span.End()
		if closer != nil {
			_ = closer.Close()
		}
	}
}

// printHeaderOnce prints the one-line correlation header the first time it is
// called for a given trace id, then records that trace id in the run sentinel so
// the later processes of the same run stay quiet. The header is the first line
// of a run's output: the log directory, the trace id, and the span id.
func printHeaderOnce(corr correlation.Context) {
	if prev, err := os.ReadFile(runSentinel); err == nil {
		if strings.TrimSpace(string(prev)) == string(corr.TraceID) {
			return
		}
	}
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.WriteFile(runSentinel, []byte(corr.TraceID), 0o644)
	// Debug keeps this boundary event below the summary handler's INFO threshold,
	// so it satisfies the boundary-log analyzer without inflating the run's one
	// diagnostics line.
	slog.Debug("run header emitted", slog.String("trace_id", string(corr.TraceID)))
	writeStderr(runHeaderLine(corr) + "\n")
}

func runHeaderLine(corr correlation.Context) string {
	return "logs=" + logDir +
		" trace_id=" + string(corr.TraceID) +
		" span_id=" + string(corr.SpanID)
}

func persistTraceparent(traceparent string) {
	if strings.TrimSpace(traceparent) == "" {
		return
	}
	slog.Debug("persist traceparent", slog.String("path", traceparentFile))
	_ = os.MkdirAll(logDir, 0o755)
	owners := sessionOwnerPIDs()
	if len(owners) == 0 {
		owners = []int{os.Getpid()}
	}
	ids := make([]string, len(owners))
	for i, pid := range owners {
		ids[i] = strconv.Itoa(pid)
	}
	body := traceparent + "\n" + strings.Join(ids, ",") + "\n"
	_ = os.WriteFile(traceparentFile, []byte(body), 0o644)
}

func loadMakeTraceparent() string {
	if os.Getenv("MAKEFLAGS") == "" && !joinsPersistedTraceparent() {
		return ""
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		body, err := os.ReadFile(filepath.Join(dir, traceparentFile))
		if err == nil {
			if got := parsePersistedTraceparent(string(body)); got != "" {
				return got
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// parsePersistedTraceparent reads the W3C TRACEPARENT and owner pids from a
// persisted file. A later make must not join a leftover file, so a missing or
// non-ancestor owner pid is ignored. Sequential nested makes from one outer
// make share that outer pid, so they still join.
func parsePersistedTraceparent(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 2 {
		return ""
	}
	traceparent := strings.TrimSpace(lines[0])
	if traceparent == "" {
		return ""
	}
	for _, field := range strings.Split(lines[1], ",") {
		owner, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || owner <= 1 {
			continue
		}
		if isAncestorPID(owner) {
			return traceparent
		}
	}
	return ""
}

func sessionOwnerPIDs() []int {
	pid := os.Getpid()
	seen := map[int]bool{}
	var owners []int
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		name, parent, err := processNameAndParent(pid)
		if err != nil {
			parent, err = parentPID(pid)
			if err != nil || parent == pid {
				break
			}
			pid = parent
			continue
		}
		if isMakeName(name) {
			owners = append(owners, pid)
		}
		pid = parent
	}
	return owners
}

func isMakeName(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	return base == "make" || base == "gmake" || base == "gnumake"
}

func isAncestorPID(want int) bool {
	if want == os.Getpid() {
		return true
	}
	pid := os.Getppid()
	seen := map[int]bool{os.Getpid(): true}
	for pid > 1 && !seen[pid] {
		if pid == want {
			return true
		}
		seen[pid] = true
		parent, err := parentPID(pid)
		if err != nil || parent == pid {
			return false
		}
		pid = parent
	}
	return pid == want
}

func parentPID(pid int) (int, error) {
	slog.Debug("lookup parent pid", slog.Int("pid", pid))
	command := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid))
	output, err := command.Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(output)))
}

func processNameAndParent(pid int) (string, int, error) {
	parent, err := parentPID(pid)
	if err != nil {
		return "", 0, err
	}
	slog.Debug("lookup process name", slog.Int("pid", pid))
	command := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid))
	output, err := command.Output()
	if err != nil {
		return "", parent, err
	}
	return strings.TrimSpace(string(output)), parent, nil
}

// joinsPersistedTraceparent reports whether this process should join a
// sibling go-mk's minted TRACEPARENT even when MAKEFLAGS is missing.
// resolve-bin runs from a helper script that may not export MAKEFLAGS.
func joinsPersistedTraceparent() bool {
	for _, arg := range os.Args[1:] {
		if arg == "-flags" || arg == "--flags" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg == "resolve-bin"
	}
	return false
}
