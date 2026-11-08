package notice

import (
	"log/slog"
	"os"
	"path/filepath"
)

// StageTools copies configuration and tools into a historical checkout.
func StageTools(source, destination string) error {
	slog.Info("notice stage tools", slog.String("source", source), slog.String("destination", destination))
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return failure("create notice staging directory", err)
	}

	entries, err := os.ReadDir(source)
	if err != nil {
		return failure("read notice staging directory", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "scripts" && entry.IsDir() {
			if err := os.CopyFS(filepath.Join(destination, name), os.DirFS(filepath.Join(source, name))); err != nil {
				return failure("copy historical notice scripts", err)
			}

			continue
		}

		if entry.IsDir() {
			continue
		}

		if name == "staticcheck-extra" {
			if err := os.Symlink(filepath.Join(source, name), filepath.Join(destination, name)); err != nil {
				return failure("link historical notice analyzer", err)
			}

			continue
		}

		extension := filepath.Ext(name)
		if extension != ".mk" && extension != ".yml" && extension != ".yaml" && extension != ".txt" && name != ".go-mk-commit" {
			continue
		}

		contents, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			return failure("read staged notice tool", err)
		}

		if err := os.WriteFile(filepath.Join(destination, name), contents, 0o600); err != nil {
			return failure("write staged notice tool", err)
		}
	}

	return nil
}
