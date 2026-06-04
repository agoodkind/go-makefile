// Package logsummary tests the stderr-collapse handler: mode parsing and the
// handler contract that INFO is dropped from the stderr stream while WARN and
// ERROR pass through to the base handler.
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

func TestHandlerSummaryDropsInfoAndPassesWarnError(t *testing.T) {
	var buf bytes.Buffer
	handler := &Handler{base: slog.NewTextHandler(&buf, nil), mode: ModeSummary}
	logger := slog.New(handler)
	logger.Info("lint read file", slog.String("path", "a.go"))
	logger.Warn("watch out")
	logger.Error("broke", slog.String("err", "boom"))

	streamed := buf.String()
	if strings.Contains(streamed, "lint read file") {
		t.Errorf("INFO should be dropped from stderr, not streamed:\n%s", streamed)
	}
	if !strings.Contains(streamed, "watch out") || !strings.Contains(streamed, "broke") {
		t.Errorf("WARN and ERROR must pass through:\n%s", streamed)
	}
}

func TestHandlerQuietDropsInfoKeepsError(t *testing.T) {
	var buf bytes.Buffer
	handler := &Handler{base: slog.NewTextHandler(&buf, nil), mode: ModeQuiet}
	logger := slog.New(handler)
	logger.Info("lint read file")
	logger.Error("broke", slog.String("err", "boom"))

	streamed := buf.String()
	if strings.Contains(streamed, "lint read file") {
		t.Errorf("quiet mode must drop INFO from stderr:\n%s", streamed)
	}
	if !strings.Contains(streamed, "broke") {
		t.Errorf("quiet mode must still stream ERROR:\n%s", streamed)
	}
}

func TestHandlerDebugStreamsInfo(t *testing.T) {
	var buf bytes.Buffer
	handler := &Handler{
		base: slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		mode: ModeDebug,
	}
	logger := slog.New(handler)
	logger.Info("lint read file", slog.String("path", "a.go"))

	if !strings.Contains(buf.String(), "lint read file") {
		t.Errorf("debug mode must stream INFO:\n%s", buf.String())
	}
}

func TestHandlerEnabledThresholds(t *testing.T) {
	summary := &Handler{base: slog.NewTextHandler(&bytes.Buffer{}, nil), mode: ModeSummary}
	if summary.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("summary mode should not enable Debug")
	}
	if !summary.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("summary mode should enable Info")
	}
}
