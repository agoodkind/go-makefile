package lint

import (
	"os"
	"strings"
)

// GolangciCommand builds the command for the requested mode.
//
// Run mode expands package targets before invoking the linter.
//
// For example, select run mode with:
//
//	GolangciCommand("run", expand)
func GolangciCommand(
	mode string,
	expand func([]string) ([]string, error),
) (string, []string, error) {
	binary := os.Getenv("GOLANGCI_LINT")
	if binary == "" {
		binary = "golangci-lint"
	}

	flags := os.Getenv("GOLANGCI_LINT_RUN_FLAGS")
	if flags == "" || mode == "fmt" {
		flags = os.Getenv("GOLANGCI_LINT_FLAGS")
	}

	targets := os.Getenv("GOLANGCI_LINT_TARGETS")
	if targets == "" {
		targets = "./..."
	}

	packages := strings.Fields(targets)
	if mode == "run" {
		var err error
		packages, err = expand(packages)
		if err != nil {
			return "", nil, err
		}
	}

	args := []string{mode}
	if mode == "run" {
		args = append(args, "--output.text.print-issued-lines=false")
	}

	args = append(args, strings.Fields(flags)...)
	args = append(args, packages...)

	return binary, args, nil
}
