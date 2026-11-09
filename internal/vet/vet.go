// Package vet runs Go vet for the selected source files.
package vet

import "goodkind.io/go-makefile/internal/report"

// Run executes each selected vet command.
//
// The first selection or execution error stops the run.
//
// For example, execute selected package commands with:
//
//	Run(commands, run)
func Run(
	commands func() ([][]string, error),
	run func(string, []string) error,
) error {
	selected, err := commands()
	if err != nil {
		return err
	}

	for _, arguments := range selected {
		if err := run("go", arguments); err != nil {
			return err
		}
	}

	return nil
}

// Step executes each selected vet command and reports its result.
//
// A failed command stops the run and supplies its diagnostic result.
//
// For example, capture the first failure with:
//
//	Step(commands, run)
func Step(
	commands func() ([][]string, error),
	run func(string, string, []string) (report.StepResult, int),
) (report.StepResult, int) {
	selected, err := commands()
	if err != nil {
		return report.StepResult{
			Name:     "vet",
			Status:   report.StatusFailed,
			Findings: []string{err.Error()},
		}, 1
	}

	for _, arguments := range selected {
		result, code := run("vet", "go", arguments)
		if code != 0 {
			return result, code
		}
	}

	return report.StepResult{Name: "vet", Status: report.StatusOK}, 0
}
