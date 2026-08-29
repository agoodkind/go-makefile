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

// runSentinel is the old header proof written by engine versions before trace
// sessions. New versions only read it while a consumer upgrades its engine.
const runSentinel = ".make/logs/.run"

// traceparentFile is the old shared trace file. New sessions never write it.
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
	claim := traceSessionClaim{}
	if inherited == "" {
		outerPID := currentOutermostMakePID()
		if outerPID > 1 {
			var claimErr error
			claim, claimErr = defaultTraceSessionStore().claim(
				outerPID,
				!headerless(),
				loadLegacyTraceparent(outerPID),
			)
			if claimErr != nil {
				slog.Debug("trace session unavailable", slog.Any("error", claimErr))
			} else if claim.traceparent != "" {
				inherited = claim.traceparent
				_ = os.Setenv("TRACEPARENT", inherited)
			}
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
	if inherited == "" && claim.owner {
		if traceparent := corr.Traceparent(); traceparent != "" {
			if err := claim.persist(traceparent); err == nil {
				_ = os.Setenv("TRACEPARENT", traceparent)
			} else {
				slog.Debug("trace session persist failed", slog.Any("error", err))
			}
		}
	}

	handler := correlation.SlogHandler(router, correlation.HandlerOptions{
		Required: []string{"trace_id", "span_id"},
	})
	slog.SetDefault(slog.New(handler.WithAttrs(corr.Attrs())))

	if inherited == "" && !headerless() {
		printHeader(corr)
	}
	claim.close()

	return func() {
		span.End()
		if closer != nil {
			_ = closer.Close()
		}
	}
}

// printHeader prints the one-line correlation header for the process that
// minted a visible trace. Session election already ensures one print per run.
func printHeader(corr correlation.Context) {
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
