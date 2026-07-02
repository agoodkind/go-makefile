package selfupdate

import (
	"path/filepath"
	"testing"
)

func TestDefaultStatePathUsesXDGStateHome(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)

	got := DefaultStatePath("demo")
	want := filepath.Join(stateHome, "demo", "update-state.json")
	if got != want {
		t.Fatalf("DefaultStatePath() = %q, want %q", got, want)
	}
}

func TestDefaultCacheDirUsesXDGCacheHome(t *testing.T) {
	cacheHome := filepath.Join(t.TempDir(), "cache")
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	got := DefaultCacheDir("demo")
	want := filepath.Join(cacheHome, "demo", "update")
	if got != want {
		t.Fatalf("DefaultCacheDir() = %q, want %q", got, want)
	}
}

func TestReleaseAPIBaseURLUsesEnvOverride(t *testing.T) {
	t.Setenv("SELFUPDATE_TEST_API_BASE_URL", "https://example.invalid/api/")

	got := releaseAPIBaseURL(Config{
		APIBaseURL:    "https://api.github.com/",
		APIBaseURLEnv: "SELFUPDATE_TEST_API_BASE_URL",
	})
	if got != "https://example.invalid/api" {
		t.Fatalf("releaseAPIBaseURL() = %q", got)
	}
}
