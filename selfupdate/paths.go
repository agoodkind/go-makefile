package selfupdate

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultStatePath returns the XDG-derived update state file path for binary.
func DefaultStatePath(binary string) string {
	return filepath.Join(defaultStateBaseDir(), binary, "update-state.json")
}

// DefaultCacheDir returns the XDG-derived update cache directory for binary.
func DefaultCacheDir(binary string) string {
	return filepath.Join(defaultCacheBaseDir(), binary, "update")
}

func defaultStateBaseDir() string {
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return base
	}
	return filepath.Join(userHomeDir(), ".local", "state")
}

func defaultCacheBaseDir() string {
	if base := os.Getenv("XDG_CACHE_HOME"); base != "" {
		return base
	}
	home := userHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches")
	}
	return filepath.Join(home, ".cache")
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err == nil {
		return home
	}
	return os.Getenv("HOME")
}
