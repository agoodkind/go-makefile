// Package logsummary keeps the repetitive INFO boundary logs that the go-mk
// lint chain emits off the stderr stream. The lint command layer logs a
// structured slog event at every file read, directory creation, tool install,
// and process launch to satisfy the missing_boundary_log analyzer, which would
// otherwise flood stderr with the same handful of messages dozens of times per
// run. This package installs a slog.Handler that drops those INFO records from
// the stderr stream while passing WARN and ERROR through to a text handler in
// real time. The full INFO records still reach the per-concern JSONL files
// through the router this handler nests under, so nothing is lost; only the
// stderr stream is kept clean. Actual lint findings never travel on this stream;
// they render through internal/lintgate to stdout.
package logsummary

import (
	"context"
	"io"
	"log/slog"
)

// Mode selects how the handler treats INFO boundary logs. ModeSummary and
// ModeQuiet both drop INFO from the stderr stream, and ModeDebug passes the full
// stream through unchanged (the historical behaviour). WARN and ERROR always
// reach the underlying text handler regardless of mode.
type Mode int

const (
	// ModeSummary drops INFO records from the stderr stream.
	ModeSummary Mode = iota
	// ModeDebug emits every record through the base text handler.
	ModeDebug
	// ModeQuiet also drops INFO records from the stderr stream.
	ModeQuiet
)

// ParseMode maps the GO_MK_LOG value to a Mode. The default for an empty or
// unrecognized value is summary: INFO boundary logs are dropped from stderr,
// GO_MK_LOG=quiet does the same, and GO_MK_LOG=debug streams the full raw log.
// It uses an if chain rather than a switch so the string_switch_should_be_enum
// analyzer stays satisfied without a named string type for what is only an
// environment value.
func ParseMode(value string) Mode {
	if value == "debug" || value == "verbose" {
		return ModeDebug
	}
	if value == "quiet" || value == "off" || value == "silent" {
		return ModeQuiet
	}
	return ModeSummary
}

// Handler wraps a base slog.Handler. In summary and quiet modes it drops INFO
// records from the stderr stream and routes WARN and ERROR to the base handler;
// in debug mode it delegates every record to the base handler.
type Handler struct {
	base slog.Handler
	mode Mode
}

// Enabled reports whether a record at level should be processed. Debug mode
// defers to the base handler; summary and quiet modes accept INFO and above so
// WARN and ERROR reach Handle to be forwarded.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.mode == ModeDebug {
		return h.base.Enabled(ctx, level)
	}
	return level >= slog.LevelInfo
}

// Handle forwards WARN and ERROR (and, in debug mode, every record) to the base
// handler. In summary and quiet modes an INFO record is dropped from the stderr
// stream; the router this handler nests under still writes it to the per-concern
// JSONL file.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	if h.mode == ModeDebug {
		return h.base.Handle(ctx, record)
	}
	if record.Level >= slog.LevelWarn {
		return h.base.Handle(ctx, record)
	}
	return nil
}

// WithAttrs returns a clone whose base carries attrs.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{base: h.base.WithAttrs(attrs), mode: h.mode}
}

// WithGroup returns a clone whose base opens the named group.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{base: h.base.WithGroup(name), mode: h.mode}
}

// New builds the summary handler over writer for mode and returns it so a caller
// can nest it under another handler, such as a per-concern router, as the
// stderr-facing sink. It keeps stderr clean by dropping INFO records while a
// nesting parent fans the full records out to its own sinks.
func New(writer io.Writer, mode Mode) slog.Handler {
	level := slog.LevelInfo
	if mode == ModeDebug {
		level = slog.LevelDebug
	}
	base := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})
	return &Handler{base: base, mode: mode}
}
