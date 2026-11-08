package notice

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
)

// StageTools copies configuration and tools into a historical checkout.
//
// The historical checkout uses the active build inputs and analyzer.
// Captured findings remain separate from the caller's current output.
//
// Staging performs these operations:
//   - Copy the shared scripts and supported configuration files.
//   - Link the current staticcheck-extra executable.
//   - Retain the current build revision metadata.
func StageTools(source, destination string) error {
	slog.Info("notice stage tools", slog.String("source", source), slog.String("destination", destination))
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return failure("create notice staging directory", err)
	}

	checkout, err := os.OpenRoot(source)
	if err != nil {
		return failure("open notice staging source", err)
	}

	defer func() { _ = checkout.Close() }()

	output, err := os.OpenRoot(destination)
	if err != nil {
		return failure("open notice staging destination", err)
	}

	defer func() { _ = output.Close() }()

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
		configuration := slices.Contains([]string{".mk", ".yml", ".yaml", ".txt"}, extension)
		if !configuration && name != ".go-mk-commit" {
			continue
		}

		contents, err := checkout.ReadFile(filepath.Base(name))
		if err != nil {
			return failure("read staged notice tool", err)
		}

		file, err := output.OpenFile(filepath.Base(name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return failure("open staged notice tool", err)
		}

		_, writeError := file.Write(contents)
		closeError := file.Close()
		if err := errors.Join(writeError, closeError); err != nil {
			return failure("write staged notice tool", err)
		}
	}

	return nil
}
