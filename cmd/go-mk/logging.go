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

// headerlessCommands are auxiliary subcommands that run as make prerequisites
// and are not a user-facing run, so they do not print the run header. notice
// prints third-party license notices ahead of build, lint, and build-check.
var headerlessCommands = map[string]bool{"notice": true}

// headerless reports whether this invocation is an auxiliary subcommand that
// should not print the run header. It reads the first non-flag argument as the
// subcommand name.
func headerless() bool {
	for _, arg := range os.Args[1:] {
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
		}
	}

	handler := correlation.SlogHandler(router, correlation.HandlerOptions{
		Required: []string{"trace_id", "span_id"},
	})
	slog.SetDefault(slog.New(handler.WithAttrs(corr.Attrs())))

	if !headerless() {
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
	writeStderr(correlation.MarkerLine(
		"logs", logDir,
		"trace_id", string(corr.TraceID),
		"span_id", string(corr.SpanID),
	) + "\n")
}
