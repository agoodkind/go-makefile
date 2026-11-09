package history

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

var errMakefileType = errors.New("historical preparation requires a regular Makefile")

func preparationMakefile(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", failed("resolve historical Makefile", err)
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", failed("resolve required historical Makefile", err)
	}

	directory, err := os.OpenRoot(filepath.Dir(resolved))
	if err != nil {
		return "", failed("open historical Makefile directory", err)
	}

	defer func() {
		if err := directory.Close(); err != nil {
			slog.Error("notice close Makefile directory", slog.Any("error", err))
		}
	}()
	info, err := directory.Stat(filepath.Base(resolved))
	if err != nil {
		return "", failed("inspect required historical Makefile", err)
	}

	if !info.Mode().IsRegular() {
		slog.Error(
			"notice require regular Makefile",
			slog.String("path", resolved),
			slog.Any("error", errMakefileType),
		)

		return "", fmt.Errorf("%w: %s", errMakefileType, resolved)
	}

	return resolved, nil
}
