package selfupdate

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// State stores the latest updater observation and result.
type State struct {
	LastCheckAt        time.Time `json:"last_check_at"`
	NextCheckAt        time.Time `json:"next_check_at"`
	LatestTag          string    `json:"latest_tag,omitempty"`
	AppliedTag         string    `json:"applied_tag,omitempty"`
	InstalledVersion   string    `json:"installed_version,omitempty"`
	InstalledCommit    string    `json:"installed_commit,omitempty"`
	InstalledBuildHash string    `json:"installed_build_hash,omitempty"`
	LastResult         string    `json:"last_result,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
}

// LoadState reads the persisted updater state file.
func LoadState(path string) (State, error) {
	log := slog.Default()
	if path == "" {
		return State{}, fmt.Errorf("update state path is required")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{
				LastCheckAt:        time.Time{},
				NextCheckAt:        time.Time{},
				LatestTag:          "",
				AppliedTag:         "",
				InstalledVersion:   "",
				InstalledCommit:    "",
				InstalledBuildHash: "",
				LastResult:         "",
				LastError:          "",
			}, nil
		}
		log.Warn("update state read failed", "path", path, "err", err)
		return State{}, fmt.Errorf("read update state: %w", err)
	}
	var state State
	if err := json.Unmarshal(content, &state); err != nil {
		log.Warn("update state decode failed", "path", path, "err", err)
		return State{}, fmt.Errorf("decode update state: %w", err)
	}
	return state, nil
}

// SaveState writes the persisted updater state file atomically.
func SaveState(path string, state State) error {
	log := slog.Default()
	if path == "" {
		return fmt.Errorf("update state path is required")
	}
	log.Info("update state save", "path", path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Warn("update state create dir failed", "path", path, "err", err)
		return fmt.Errorf("create update state dir: %w", err)
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Warn("update state encode failed", "path", path, "err", err)
		return fmt.Errorf("encode update state: %w", err)
	}
	content = append(content, '\n')
	tempFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		log.Warn("update state temp create failed", "path", path, "err", err)
		return fmt.Errorf("create update state temp: %w", err)
	}
	tmpPath := tempFile.Name()
	if _, err := tempFile.Write(content); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tmpPath)
		log.Warn("update state temp write failed", "path", tmpPath, "err", err)
		return fmt.Errorf("write update state temp: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		log.Warn("update state temp close failed", "path", tmpPath, "err", err)
		return fmt.Errorf("close update state temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		log.Warn("update state replace failed", "path", path, "err", err)
		return fmt.Errorf("replace update state: %w", err)
	}
	return nil
}
