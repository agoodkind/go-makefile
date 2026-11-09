// Package format applies the shared source formatting checks.
package format

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"goodkind.io/go-makefile/internal/lint"
)

var (
	errMissingAnalyzer = errors.New("make fmt requires staticcheck-extra")
	errForeignSource   = errors.New("selected formatting source is outside the current module")
)

// Run applies argument layout, whitespace, and the configured Go formatters.
//
// Callbacks reuse the existing tool preparation and module discovery.
// Only the selected module files receive fixes.
//
// The command sequence is:
//  1. Apply whitespace fixes to the selected files.
//  2. Run the configured Go formatters.
//  3. Apply the supplied multiline argument formatter.
func Run(
	prepare func() error,
	resolve func() error,
	selectAnalyzer func() (string, error),
	listFiles func([]string) ([]string, error),
	run func(string, []string) error,
) error {
	if err := prepare(); err != nil {
		return err
	}

	files := strings.Fields(os.Getenv("GOLANGCI_FMT_FILES"))
	if len(files) == 0 {
		targets := strings.Fields(os.Getenv("GOLANGCI_LINT_TARGETS"))
		if len(targets) == 0 {
			targets = []string{"./..."}
		}

		var err error
		files, err = listFiles(targets)
		if err != nil {
			return err
		}
	}

	if len(files) == 0 {
		return nil
	}

	if err := resolve(); err != nil {
		return err
	}

	analyzer, err := selectAnalyzer()
	if err != nil {
		return err
	}

	if analyzer == "" {
		return errMissingAnalyzer
	}

	cwd, err := os.Getwd()
	if err != nil {
		return failure("read formatting directory", err)
	}

	for index, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			return failure("resolve formatting source", err)
		}

		relative, err := filepath.Rel(cwd, absolute)
		if err != nil {
			return failure("resolve module formatting source", err)
		}

		if !filepath.IsLocal(relative) {
			wrapped := fmt.Errorf("%w: %s", errForeignSource, file)
			slog.Error("select formatting source", slog.Any("err", wrapped))

			return wrapped
		}

		files[index] = filepath.ToSlash(relative)
	}

	packages := lint.ScopedPackagesFromFiles(files)
	patch, err := selectionPatch(files)
	if err != nil {
		return err
	}
	defer removePatch(patch)
	binary := os.Getenv("GOLANGCI_LINT")
	if binary == "" {
		binary = "golangci-lint"
	}

	flags := strings.Fields(os.Getenv("GOLANGCI_LINT_FLAGS"))
	fix := []string{"run", "--fix", "--enable-only=wsl_v5", "--new-from-patch", patch, "--whole-files"}
	if err := run(binary, slices.Concat(fix, flags, packages)); err != nil {
		return failure("format whitespace", err)
	}

	if err := run(binary, slices.Concat([]string{"fmt"}, flags, files)); err != nil {
		return err
	}

	return run(analyzer, append([]string{"--format-multilineargs"}, files...))
}

func failure(operation string, err error) error {
	wrapped := fmt.Errorf("%s: %w", operation, err)
	slog.Error("formatting failed", slog.String("operation", operation), slog.Any("err", wrapped))

	return wrapped
}

func selectionPatch(files []string) (string, error) {
	slog.Info("select formatting files", slog.Int("count", len(files)))
	root, err := os.OpenRoot(".")
	if err != nil {
		return "", failure("open formatting directory", err)
	}

	defer func() { _ = root.Close() }()
	var patch strings.Builder

	for _, file := range files {
		contents, err := root.ReadFile(file)
		if err != nil {
			return "", failure("read formatting source", err)
		}

		lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
		fmt.Fprintf(&patch, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", file, len(lines))

		for _, line := range lines {
			patch.WriteString("+" + line + "\n")
		}
	}

	file, err := os.CreateTemp("", "go-mk-format-*.patch")
	if err != nil {
		return "", failure("create formatting selection", err)
	}

	_, writeErr := file.WriteString(patch.String())
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		removePatch(file.Name())
		return "", err
	}

	return file.Name(), nil
}

func removePatch(path string) {
	directory, name := filepath.Split(path)
	root, err := os.OpenRoot(directory)
	if err != nil {
		slog.Error("open formatting patch directory", slog.Any("err", err))
		return
	}

	defer func() { _ = root.Close() }()
	if err := root.Remove(name); err != nil {
		slog.Error("remove formatting patch", slog.Any("err", err))
	}
}
