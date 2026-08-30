// Logging setup for go-mk. Every run fans structured records out to
// per-concern JSONL files under logDir and, collapsed, to the summary stream on
// stderr.
package main

import (
	"log/slog"
	"os"

	"goodkind.io/gklog"
	"goodkind.io/go-makefile/internal/logsummary"
)

// logDir is the per-concern JSONL directory every run writes under.
const logDir = ".make/logs"

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
