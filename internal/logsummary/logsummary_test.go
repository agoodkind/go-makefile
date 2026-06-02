// Package logsummary tests the dedup handler and its summary rendering: mode
// parsing, the count-to-sentence rollup including singular/plural and the
// fallback wording, message-to-bucket merging, and the handler contract that
// INFO is collapsed while WARN and ERROR pass through to the base handler.
package logsummary

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"":        ModeSummary,
		"bogus":   ModeSummary,
		"summary": ModeSummary,
		"quiet":   ModeQuiet,
		"off":     ModeQuiet,
		"silent":  ModeQuiet,
		"debug":   ModeDebug,
		"verbose": ModeDebug,
	}
	for value, want := range cases {
		if got := ParseMode(value); got != want {
			t.Errorf("ParseMode(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestOneLineOrdersByCountThenText(t *testing.T) {
	got := OneLine(map[string]int{
		"lint read file":         11,
		"lint install go tool":   3,
		"lint run gate via make": 1,
	})
	want := "read 11 files, installed 3 Go tools, ran 1 gate"
	if got != want {
		t.Errorf("OneLine = %q, want %q", got, want)
	}
}

func TestOneLineLowercaseJoined(t *testing.T) {
	got := OneLine(map[string]int{
		"lint read file":       11,
		"lint install go tool": 3,
	})
	want := "read 11 files, installed 3 Go tools"
	if got != want {
		t.Errorf("OneLine = %q, want %q", got, want)
	}
	if OneLine(map[string]int{}) != "" {
		t.Error("OneLine of empty counts should be empty")
	}
}

func TestOneLineSingularAndMerge(t *testing.T) {
	if got := OneLine(map[string]int{"lint read file": 1}); got != "read 1 file" {
		t.Errorf("singular OneLine = %q, want %q", got, "read 1 file")
	}
	merged := OneLine(map[string]int{
		"lint read file":         2,
		"lint read file content": 3,
	})
	if merged != "read 5 files" {
		t.Errorf("merge OneLine = %q, want %q", merged, "read 5 files")
	}
}

func TestOneLineFallbackSentence(t *testing.T) {
	got := OneLine(map[string]int{"lint evaluate bypass token": 2})
	if got != "evaluate bypass token: 2 times" {
		t.Errorf("fallback OneLine = %q, want %q", got, "evaluate bypass token: 2 times")
	}
}

func TestHandlerSummaryCollapsesInfoAndPassesWarnError(t *testing.T) {
	var buf bytes.Buffer
	handler := &Handler{
		base:    slog.NewTextHandler(&buf, nil),
		mode:    ModeSummary,
		counter: newCounter(),
	}
	logger := slog.New(handler)
	logger.Info("lint read file", slog.String("path", "a.go"))
	logger.Info("lint read file", slog.String("path", "b.go"))
	logger.Warn("watch out")
	logger.Error("broke", slog.String("err", "boom"))

	streamed := buf.String()
	if strings.Contains(streamed, "lint read file") {
		t.Errorf("INFO should be collapsed, not streamed:\n%s", streamed)
	}
	if !strings.Contains(streamed, "watch out") || !strings.Contains(streamed, "broke") {
		t.Errorf("WARN and ERROR must pass through:\n%s", streamed)
	}
	if got := handler.counter.snapshot()["lint read file"]; got != 2 {
		t.Errorf("collapsed INFO count = %d, want 2", got)
	}
}

func TestHandlerQuietDropsInfoKeepsError(t *testing.T) {
	var buf bytes.Buffer
	handler := &Handler{
		base:    slog.NewTextHandler(&buf, nil),
		mode:    ModeQuiet,
		counter: newCounter(),
	}
	logger := slog.New(handler)
	logger.Info("lint read file")
	logger.Error("broke", slog.String("err", "boom"))

	if got := len(handler.counter.snapshot()); got != 0 {
		t.Errorf("quiet mode must not count INFO, got %d entries", got)
	}
	if !strings.Contains(buf.String(), "broke") {
		t.Errorf("quiet mode must still stream ERROR:\n%s", buf.String())
	}
}

func TestHandlerDebugStreamsInfo(t *testing.T) {
	var buf bytes.Buffer
	handler := &Handler{
		base:    slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		mode:    ModeDebug,
		counter: newCounter(),
	}
	logger := slog.New(handler)
	logger.Info("lint read file", slog.String("path", "a.go"))

	if !strings.Contains(buf.String(), "lint read file") {
		t.Errorf("debug mode must stream INFO:\n%s", buf.String())
	}
	if got := len(handler.counter.snapshot()); got != 0 {
		t.Errorf("debug mode must not collapse, got %d entries", got)
	}
}

func TestHandlerEnabledThresholds(t *testing.T) {
	summary := &Handler{base: slog.NewTextHandler(&bytes.Buffer{}, nil), mode: ModeSummary, counter: newCounter()}
	if summary.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("summary mode should not enable Debug")
	}
	if !summary.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("summary mode should enable Info")
	}
}
