package selfupdate

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSchedulerRecoversPanicPerIteration(t *testing.T) {
	originalTimeNow := timeNow
	originalFetchLatestRelease := updateFetchLatestRelease
	t.Cleanup(func() {
		timeNow = originalTimeNow
		updateFetchLatestRelease = originalFetchLatestRelease
	})

	fakeNow := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	timeNow = func() time.Time {
		return fakeNow
	}
	statePath := filepath.Join(t.TempDir(), "update.json")
	if err := SaveState(statePath, State{NextCheckAt: fakeNow.Add(time.Nanosecond)}); err != nil {
		t.Fatalf("SaveState() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	secondCall := make(chan struct{})
	var callCount atomic.Int32
	updateFetchLatestRelease = func(_ context.Context, _ Options) (release, error) {
		if callCount.Add(1) == 1 {
			panic("scheduled check panic")
		}
		cancel()
		close(secondCall)
		return release{
			TagName: testCurrentVersion,
			Assets: []releaseAsset{
				{
					Name:               "agent-gate_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz",
					BrowserDownloadURL: "https://example.invalid/archive",
				},
			},
		}, nil
	}

	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		RunScheduler(ctx, SchedulerHooks{
			Enabled: func() bool {
				return true
			},
			Mode: func() string {
				return ModeCheck
			},
			Options: func() Options {
				return Options{
					Config:    testConfig(),
					StatePath: statePath,
				}
			},
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()

	select {
	case <-secondCall:
	case <-schedulerDone:
		t.Fatalf("RunScheduler exited after panic; update calls = %d, want second iteration", callCount.Load())
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for second scheduler iteration; update calls = %d", callCount.Load())
	}
	select {
	case <-schedulerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScheduler did not stop after context cancellation")
	}
}
