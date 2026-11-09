package format

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"goodkind.io/go-makefile/internal/report"
)

const gateName = "lint-format"

// RunGate checks the complete formatting sequence without rewriting source.
//
// Existing command operations prepare tools and render the gate result.
// Formatter and temporary patch failures always fail the gate.
//
// The command layer delegates its boundary operations:
//
//	RunGate(directory, ensure, tools, command, resolve, analyzer, capture,
//	    collecting, marker, failed, failure, stdout, status)
func RunGate(
	directory string,
	ensure func() error,
	tools func() error,
	command func() (Command, error),
	resolve func() error,
	analyzer func() (string, error),
	capture func(string, []string, string) (int, error),
	collecting bool,
	marker func(report.GateMarker),
	failed func(string),
	failure func([]string),
	stdout func(string),
	status func(error) int,
) int {
	if err := ensure(); err != nil {
		return status(err)
	}

	if err := tools(); err != nil {
		return status(err)
	}

	selected, err := command()
	if errors.Is(err, ErrNoFiles) {
		if collecting {
			marker(report.GateMarker{
				Name:        gateName,
				Passed:      true,
				Findings:    nil,
				Remediation: "",
			})
		} else {
			stdout("lint-format: OK\n  No Go source files in this module.\n")
		}

		return 0
	}

	if err != nil {
		return status(err)
	}

	output := filepath.Join(directory, "lint-format.out")
	result, err := Verify(selected, output, resolve, analyzer, capture)
	if err != nil {
		return status(err)
	}

	if len(result.Changed) == 0 {
		if collecting {
			marker(report.GateMarker{
				Name:        gateName,
				Passed:      true,
				Findings:    nil,
				Remediation: "",
			})
		}

		return 0
	}

	text, err := os.ReadFile(output)
	if err != nil {
		return status(err)
	}

	failed(gateName)
	if collecting {
		failure(
			strings.Split("Source formatters need to update:\n"+string(text)+"run make fmt", "\n"),
		)
	} else {
		stdout("Source formatters need to update:\n" + string(text) + "run make fmt\n")
	}

	return 1
}
