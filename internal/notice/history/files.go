package history

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func readFindings(path string) ([]string, error) {
	slog.Info("lint read file", slog.String("path", path))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, failed("read notice findings "+path, err)
	}

	trimmed := strings.TrimSuffix(string(data), "\n")
	if trimmed == "" {
		return []string{}, nil
	}

	return strings.Split(trimmed, "\n"), nil
}

func writeFindings(path string, lines []string) error {
	slog.Info("lint write findings file", slog.String("path", path))
	var contents strings.Builder

	for _, line := range lines {
		contents.WriteString(line + "\n")
	}

	directory, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return failed("open notice findings directory", err)
	}

	defer func() {
		if err := directory.Close(); err != nil {
			slog.Error("notice close findings directory", slog.Any("error", err))
		}
	}()
	if err := directory.WriteFile(
		filepath.Base(path),
		[]byte(contents.String()),
		0o644,
	); err != nil {
		return failed("write notice findings "+path, err)
	}

	return nil
}
