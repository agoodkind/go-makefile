// Package format applies the shared Go formatting rules.
package format

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

var errNoGoFiles = errors.New("no go files in module")

type command struct {
	binary string
	args   []string
	files  []string
}

// Command builds the formatter command for the module's selected source files.
func Command(
	listFiles func([]string) ([]string, error),
	noFiles error,
) (string, []string, error) {
	selected, err := plan(listFiles, noFiles)
	if err != nil {
		return "", nil, err
	}

	return selected.binary, selected.args, nil
}

func plan(
	listFiles func([]string) ([]string, error),
	noFiles error,
) (command, error) {
	binary := os.Getenv("GOLANGCI_LINT")
	if binary == "" {
		binary = "golangci-lint"
	}

	files := strings.Fields(os.Getenv("GOLANGCI_FMT_FILES"))
	if len(files) == 0 {
		targets := strings.Fields(os.Getenv("GOLANGCI_LINT_TARGETS"))
		if len(targets) == 0 {
			targets = []string{"./..."}
		}

		resolved, err := listFiles(targets)
		if err != nil {
			return command{}, failure("list Go source for formatting", err)
		}

		files = resolved
	}

	if len(files) == 0 {
		return command{}, noFiles
	}

	args := []string{"fmt"}
	args = append(args, strings.Fields(os.Getenv("GOLANGCI_LINT_FLAGS"))...)
	args = append(args, files...)

	return command{binary: binary, args: args, files: files}, nil
}

// Run applies argument and whitespace fixes before the existing formatters.
func Run(
	ensureTools func() error,
	resolveAnalyzer func() error,
	selectedAnalyzer func() (string, error),
	listFiles func([]string) ([]string, error),
	run func(string, []string) error,
) error {
	if err := ensureTools(); err != nil {
		return failure("prepare formatting tools", err)
	}

	selected, err := plan(listFiles, errNoGoFiles)
	if errors.Is(err, errNoGoFiles) {
		return nil
	}

	if err != nil {
		return err
	}

	if err := resolveAnalyzer(); err != nil {
		return failure("prepare argument formatter", err)
	}

	analyzer, err := selectedAnalyzer()
	if err != nil {
		return failure("select argument formatter", err)
	}

	if analyzer == "" {
		return fmt.Errorf("make fmt requires the staticcheck-extra formatter")
	}

	if err := run(analyzer, append([]string{"-format-arguments"}, selected.files...)); err != nil {
		return failure("format argument lists", err)
	}

	if err := whitespace(selected.binary, selected.files, run); err != nil {
		return err
	}

	if err := run(selected.binary, selected.args); err != nil {
		return failure("run Go formatters", err)
	}

	return nil
}

func failure(message string, err error) error {
	wrapped := fmt.Errorf("%s: %w", message, err)
	slog.Error(message, slog.Any("err", wrapped))

	return wrapped
}
