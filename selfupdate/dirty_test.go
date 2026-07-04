package selfupdate

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCheckDirtyBuildIsNeverUpdatable verifies that a dev or locally-built
// binary (CurrentDirty) is never reported as updatable, even when a newer
// release exists, while an otherwise identical clean build is.
func TestCheckDirtyBuildIsNeverUpdatable(t *testing.T) {
	original := updateFetchLatestRelease
	t.Cleanup(func() { updateFetchLatestRelease = original })

	asset := releaseAsset{
		Name:               "demo_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz",
		BrowserDownloadURL: "https://example.test/demo.tar.gz",
	}
	updateFetchLatestRelease = func(_ context.Context, _ Options) (release, error) {
		return release{TagName: "202607032307-8-abcdef0", Assets: []releaseAsset{asset}}, nil
	}

	baseConfig := Config{
		Repo:             "agoodkind/demo",
		Binary:           "demo",
		CurrentVersion:   "202601010000-1-0000000",
		CurrentCommit:    "0000000",
		CurrentBuildHash: "abcdef012345",
		ValidateArgs:     []string{"version"},
		ValidateMatch:    "demo",
	}
	stateDir := t.TempDir()

	cleanConfig := baseConfig
	cleanResult, err := Check(context.Background(), Options{
		Config:    cleanConfig,
		StatePath: filepath.Join(stateDir, "clean.json"),
	})
	if err != nil {
		t.Fatalf("clean check: %v", err)
	}
	if !cleanResult.UpdateAvailable || cleanResult.DevBuild {
		t.Fatalf("clean build UpdateAvailable=%v DevBuild=%v, want true/false", cleanResult.UpdateAvailable, cleanResult.DevBuild)
	}

	dirtyConfig := baseConfig
	dirtyConfig.CurrentDirty = true
	dirtyResult, err := Check(context.Background(), Options{
		Config:    dirtyConfig,
		StatePath: filepath.Join(stateDir, "dirty.json"),
	})
	if err != nil {
		t.Fatalf("dirty check: %v", err)
	}
	if dirtyResult.UpdateAvailable || !dirtyResult.DevBuild {
		t.Fatalf("dirty build UpdateAvailable=%v DevBuild=%v, want false/true", dirtyResult.UpdateAvailable, dirtyResult.DevBuild)
	}
}
