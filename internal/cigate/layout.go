package cigate

import (
	"encoding/json"
	"fmt"
	"os"
)

type entry struct {
	Name                  string `json:"name"`
	Command               string `json:"command"`
	SecondaryCommand      string `json:"secondary_command"`
	ContinueOnError       bool   `json:"continue_on_error"`
	InstallGolangciLint   bool   `json:"install_golangci_lint"`
	GolangciLintCacheSlug string `json:"lint_cache_slug"`
	usesCgo               bool
}

type matrix struct {
	Include []entry `json:"include"`
}

type layoutMode string

const (
	parallelLayout    layoutMode = "parallel"
	splitLayout       layoutMode = "split"
	singleLayout      layoutMode = "single"
	qualityGateLayout layoutMode = "quality-gate"
)

const (
	buildCheckCommand = "make build-check"
	golangciGate      = "lint-golangci"
)

var testGate = entry{Name: "Test", Command: "make test", usesCgo: true}

var buildCheck = entry{
	Name:                  "Quality",
	Command:               buildCheckCommand,
	InstallGolangciLint:   true,
	GolangciLintCacheSlug: "golangci",
	usesCgo:               true,
}

var parallelGates = []entry{
	{Name: "Quality / Vet", Command: "make vet", usesCgo: true},
	{Name: "Quality / Test", Command: testGate.Command, usesCgo: testGate.usesCgo},
	{
		Name:                  "Quality / Golangci Lint",
		Command:               "make " + golangciGate,
		InstallGolangciLint:   true,
		GolangciLintCacheSlug: "golangci",
		usesCgo:               true,
	},
	{Name: "Quality / Format", Command: "make lint-format", InstallGolangciLint: true},
	{Name: "Quality / Gocyclo", Command: "make lint-gocyclo"},
	{
		Name:            "Quality / Deadcode",
		Command:         "make lint-deadcode",
		ContinueOnError: true,
		usesCgo:         true,
	},
	{Name: "Quality / Staticcheck Extra", Command: "make staticcheck-extra", usesCgo: true},
	{
		Name:            "Quality / Govulncheck",
		Command:         "make govulncheck",
		ContinueOnError: true,
		usesCgo:         true,
	},
	{Name: "Quality / Go Version", Command: "make go-version-check"},
}

func gateForTarget(target string) (entry, error) {
	command := "make " + target
	if command == buildCheck.Command {
		return buildCheck, nil
	}

	for _, gate := range parallelGates {
		if gate.Command == command {
			return gate, nil
		}
	}

	return entry{}, fmt.Errorf("unsupported go-makefile quality gate %q", target)
}

func jobLayout(layout layoutMode) (matrix, error) {
	switch layout {
	case parallelLayout:
		return matrix{Include: parallelGates}, nil
	case splitLayout:
		return matrix{Include: []entry{testGate, buildCheck}}, nil
	case singleLayout:
		checks := buildCheck
		checks.Name = "Checks"
		checks.SecondaryCommand = testGate.Command
		return matrix{Include: []entry{checks}}, nil
	default:
		return matrix{}, fmt.Errorf(
			"invalid job_layout %q: expected parallel, split, or single",
			layout,
		)
	}
}

func writeJobLayout(layout layoutMode) error {
	jobs, err := jobLayout(layout)
	if err != nil {
		return err
	}

	data, err := json.Marshal(jobs)
	if err != nil {
		return err
	}

	return appendOutputs("matrix=" + string(data) + "\n")
}

func appendOutputs(values string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return fmt.Errorf("GITHUB_OUTPUT is required")
	}

	output, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	defer func() { _ = output.Close() }()

	if _, err := output.WriteString(values); err != nil {
		return err
	}

	return nil
}
