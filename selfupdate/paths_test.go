package selfupdate_test

import (
	"path/filepath"
	"testing"

	"goodkind.io/go-makefile/selfupdate"
)

func TestDefaultStatePathUsesXDGStateHome(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)

	got := selfupdate.DefaultStatePath("demo")
	want := filepath.Join(stateHome, "demo", "update-state.json")
	if got != want {
		t.Fatalf("DefaultStatePath() = %q, want %q", got, want)
	}
}

func TestDefaultCacheDirUsesXDGCacheHome(t *testing.T) {
	cacheHome := filepath.Join(t.TempDir(), "cache")
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	got := selfupdate.DefaultCacheDir("demo")
	want := filepath.Join(cacheHome, "demo", "update")
	if got != want {
		t.Fatalf("DefaultCacheDir() = %q, want %q", got, want)
	}
}
