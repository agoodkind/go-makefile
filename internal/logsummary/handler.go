// Package logsummary collapses the repetitive INFO boundary logs that the
// go-mk lint chain emits into a single readable summary per process. The lint
// command layer logs a structured slog event at every file read, directory
// creation, tool install, and process launch to satisfy the
// missing_boundary_log analyzer, which floods stderr with the same handful of
// messages dozens of times per run. This package installs a slog.Handler that
// counts those INFO records by message instead of printing each one, passes
// WARN and ERROR through to a text handler in real time, and flushes one
// counted summary when the process exits. Actual lint findings never travel on
// this stream; they render through internal/lintgate to stdout, so collapsing
// the INFO stream cannot hide a violation.
package logsummary

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"sync"
)

// Mode selects how the handler treats INFO boundary logs. ModeSummary counts
// them and prints one summary at exit, ModeDebug passes the full stream through
// unchanged (the historical behaviour), and ModeQuiet drops them entirely.
// WARN and ERROR always reach the underlying text handler regardless of mode.
type Mode int

const (
	// ModeSummary collapses INFO records into the exit summary.
	ModeSummary Mode = iota
	// ModeDebug emits every record through the base text handler.
	ModeDebug
	// ModeQuiet discards INFO records and prints no summary.
	ModeQuiet
)

// ParseMode maps the GO_MK_LOG value to a Mode. The default for an empty or
// unrecognized value is summary: INFO boundary logs are collapsed and counted so
// the chain can render them as one diagnostics footnote, while GO_MK_LOG=quiet
// drops them and GO_MK_LOG=debug streams the full raw log. It uses an if chain
// rather than a switch so the string_switch_should_be_enum analyzer stays
// satisfied without a named string type for what is only an environment value.
func ParseMode(value string) Mode {
	if value == "debug" || value == "verbose" {
		return ModeDebug
	}
	if value == "quiet" || value == "off" || value == "silent" {
		return ModeQuiet
	}
	return ModeSummary
}

// counter accumulates INFO record counts keyed by the slog message. The handler
// is shared by the default logger and any WithAttrs/WithGroup clones, so the
// map is guarded by a mutex.
type counter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCounter() *counter {
	return &counter{counts: make(map[string]int)}
}

// add records one occurrence of message.
func (c *counter) add(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[message]++
}

// snapshot returns a copy of the current counts so rendering does not hold the
// lock while formatting.
func (c *counter) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.counts))
	maps.Copy(out, c.counts)
	return out
}

// Handler wraps a base slog.Handler. In summary and quiet modes it intercepts
// INFO records and routes WARN and ERROR to the base handler; in debug mode it
// delegates every record to the base handler. Clones from WithAttrs and
// WithGroup share the same counter so counts aggregate across the run.
type Handler struct {
	base    slog.Handler
	mode    Mode
	counter *counter
}

// Enabled reports whether a record at level should be processed. Debug mode
// defers to the base handler; summary and quiet modes accept INFO and above so
// INFO records reach Handle to be counted and WARN/ERROR reach Handle to be
// forwarded.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.mode == ModeDebug {
		return h.base.Enabled(ctx, level)
	}
	return level >= slog.LevelInfo
}

// Handle forwards WARN and ERROR (and, in debug mode, every record) to the base
// handler. In summary mode an INFO record is counted by message and dropped; in
// quiet mode it is dropped without counting.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	if h.mode == ModeDebug {
		return h.base.Handle(ctx, record)
	}
	if record.Level >= slog.LevelWarn {
		return h.base.Handle(ctx, record)
	}
	if record.Level == slog.LevelInfo && h.mode == ModeSummary {
		h.counter.add(record.Message)
	}
	return nil
}

// WithAttrs returns a clone whose base carries attrs and that shares this
// handler's counter.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{base: h.base.WithAttrs(attrs), mode: h.mode, counter: h.counter}
}

// WithGroup returns a clone whose base opens the named group and that shares
// this handler's counter.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{base: h.base.WithGroup(name), mode: h.mode, counter: h.counter}
}

// active is the installed handler. It is package level because the codebase logs
// through the global default logger rather than an injected one, so main installs
// once and the chain reads the counts back through Counts.
var active *Handler

// Install builds a text base handler over writer, wraps it in the summary
// handler for mode, and sets it as the slog default. main calls this once at
// startup with os.Stderr. The collapsed INFO counts are read back with Counts
// and rendered as the one diagnostics footnote in the run report.
func Install(writer io.Writer, mode Mode) {
	level := slog.LevelInfo
	if mode == ModeDebug {
		level = slog.LevelDebug
	}
	base := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})
	active = &Handler{base: base, mode: mode, counter: newCounter()}
	slog.SetDefault(slog.New(active))
}

// Counts returns a snapshot of the active handler's collapsed boundary-log
// counts, or nil when nothing is installed. A gate child includes these in its
// result marker so the parent can fold them into the run's one diagnostics line.
func Counts() map[string]int {
	if active == nil {
		return nil
	}
	return active.counter.snapshot()
}
