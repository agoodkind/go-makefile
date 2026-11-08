// Package format applies the shared Go formatting rules.
package format

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
)

var (
	errNoGoFiles           = errors.New("no go files in module")
	errNoArgumentFormatter = errors.New("make fmt requires the staticcheck-extra formatter")
)

type command struct {
	binary string
	args   []string
	files  []string
}

// Command builds the formatter command for the module's selected source files.
//
// GOLANGCI_FMT_FILES overrides module discovery. A module without Go source
// returns the supplied noFiles error.
//
// With two explicit files and a configuration flag, the arguments have this
// layout:
//
//	fmt -c golangci.yml main.go options.go
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

	flags := strings.Fields(os.Getenv("GOLANGCI_LINT_FLAGS"))
	args := slices.Concat([]string{"fmt"}, flags, files)

	return command{binary: binary, args: args, files: files}, nil
}

// Run formats the module's selected Go source.
//
// The callbacks prepare tools, discover module files, and execute commands.
// Run stops after the first failed operation.
//
// Run uses this order:
//
//  1. Format argument lists and callback declarations.
//  2. Apply the configured whitespace fixes.
//  3. Run the configured Go formatters.
func Run(
	ensureTools func() error,
	resolveAnalyzer func() error,
	selectedAnalyzer func() (string, error),
	listFiles func([]string) ([]string, error),
	run func(string, []string) error,
) error {
	err := ensureTools()
	if err != nil {
		return failure("prepare formatting tools", err)
	}

	selected, err := plan(listFiles, errNoGoFiles)
	if errors.Is(err, errNoGoFiles) {
		return nil
	}

	if err != nil {
		return err
	}

	err = resolveAnalyzer()
	if err != nil {
		return failure("prepare argument formatter", err)
	}

	analyzer, err := selectedAnalyzer()
	if err != nil {
		return failure("select argument formatter", err)
	}

	if analyzer == "" {
		return errNoArgumentFormatter
	}

	configuration, _, tags, err := whitespaceConfiguration()
	if err != nil {
		return failure("read argument formatter build tags", err)
	}
	defer removeTemporary(configuration)

	prefix := []string{"GO_MK_FORMAT_BUILD_TAGS=" + strings.Join(tags, ","), analyzer, "-format-arguments"}
	arguments := slices.Concat(prefix, selected.files)
	err = run("env", arguments)
	if err != nil {
		return failure("format argument lists", err)
	}

	err = whitespace(selected.binary, selected.files, run)
	if err != nil {
		return err
	}

	err = run(selected.binary, selected.args)
	if err != nil {
		return failure("run Go formatters", err)
	}

	return nil
}

func failure(message string, err error) error {
	wrapped := fmt.Errorf("%s: %w", message, err)
	slog.Error("formatting failed", slog.String("operation", message), slog.Any("err", wrapped))

	return wrapped
}
