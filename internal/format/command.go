package format

import (
	"errors"
	"os"
	"strings"
)

// ErrNoFiles indicates that the selected module does not contain Go source.
var ErrNoFiles = errors.New("no go files in module")

// Command selects the configured formatter and its exact source files.
type Command struct {
	Binary string
	Args   []string
	Files  []string
}

// Select builds a configured formatter command for the selected module files.
//
// An explicit file list overrides package discovery. Empty modules return
// ErrNoFiles rather than asking the formatter to inspect another directory.
//
// The command layer supplies its existing discovery operation:
//
//	command, err := Select(moduleGoFiles)
func Select(listFiles func([]string) ([]string, error)) (Command, error) {
	files := strings.Fields(os.Getenv("GOLANGCI_FMT_FILES"))
	if len(files) == 0 {
		targets := strings.Fields(os.Getenv("GOLANGCI_LINT_TARGETS"))
		if len(targets) == 0 {
			targets = []string{"./..."}
		}

		var err error
		files, err = listFiles(targets)
		if err != nil {
			return Command{}, err
		}
	}

	if len(files) == 0 {
		return Command{}, ErrNoFiles
	}

	return commandForFiles(files), nil
}

func commandForFiles(files []string) Command {
	binary := os.Getenv("GOLANGCI_LINT")
	if binary == "" {
		binary = "golangci-lint"
	}

	args := append([]string{"fmt"}, strings.Fields(os.Getenv("GOLANGCI_LINT_FLAGS"))...)
	args = append(args, files...)

	return Command{Binary: binary, Args: args, Files: files}
}
