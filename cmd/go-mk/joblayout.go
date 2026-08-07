package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type ciJobLayoutEntry struct {
	Name                  string `json:"name"`
	Command               string `json:"command"`
	SecondaryCommand      string `json:"secondary_command"`
	InstallGolangciLint   bool   `json:"install_golangci_lint"`
	GolangciLintCacheSlug string `json:"lint_cache_slug"`
}

type ciJobLayoutMatrix struct {
	Include []ciJobLayoutEntry `json:"include"`
}

type ciJobLayout string

const (
	ciJobLayoutParallel ciJobLayout = "parallel"
	ciJobLayoutSplit    ciJobLayout = "split"
	ciJobLayoutSingle   ciJobLayout = "single"
)

func runCIJobLayout() int {
	layout := ciJobLayout(os.Getenv("GO_MK_JOB_LAYOUT"))
	matrix, err := buildCIJobLayout(layout)
	if err != nil {
		writeStderr(err.Error() + "\n")
		return 1
	}
	matrixJSON, err := json.Marshal(matrix)
	if err != nil {
		writeStderr("go-mk-ci-job-layout: encode matrix: " + err.Error() + "\n")
		return 1
	}
	outputPath := os.Getenv("GITHUB_OUTPUT")
	if outputPath == "" {
		writeStderr("go-mk-ci-job-layout: GITHUB_OUTPUT is required\n")
		return 1
	}
	output, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		writeStderr("go-mk-ci-job-layout: open GITHUB_OUTPUT: " + err.Error() + "\n")
		return 1
	}
	defer func() {
		_ = output.Close()
	}()
	if _, err := fmt.Fprintf(output, "matrix=%s\n", matrixJSON); err != nil {
		writeStderr("go-mk-ci-job-layout: write GITHUB_OUTPUT: " + err.Error() + "\n")
		return 1
	}
	return 0
}

func buildCIJobLayout(layout ciJobLayout) (ciJobLayoutMatrix, error) {
	switch layout {
	case ciJobLayoutParallel:
		return ciJobLayoutMatrix{Include: []ciJobLayoutEntry{
			{Name: "Quality / Vet", Command: "make vet"},
			{Name: "Quality / Test", Command: "make test"},
			{Name: "Quality / Golangci Lint", Command: "make lint-golangci", InstallGolangciLint: true, GolangciLintCacheSlug: "golangci"},
			{Name: "Quality / Format", Command: "make lint-format", InstallGolangciLint: true},
			{Name: "Quality / Gocyclo", Command: "make lint-gocyclo"},
			{Name: "Quality / Deadcode", Command: "make lint-deadcode"},
			{Name: "Quality / Staticcheck Extra", Command: "make staticcheck-extra"},
			{Name: "Quality / Govulncheck", Command: "make govulncheck"},
			{Name: "Quality / Go Version", Command: "make go-version-check"},
		}}, nil
	case ciJobLayoutSplit:
		return ciJobLayoutMatrix{Include: []ciJobLayoutEntry{
			{Name: "Test", Command: "make test"},
			{Name: "Quality", Command: "make build-check", InstallGolangciLint: true, GolangciLintCacheSlug: "golangci"},
		}}, nil
	case ciJobLayoutSingle:
		return ciJobLayoutMatrix{Include: []ciJobLayoutEntry{
			{Name: "Checks", Command: "make build-check", SecondaryCommand: "make test", InstallGolangciLint: true, GolangciLintCacheSlug: "golangci"},
		}}, nil
	default:
		return ciJobLayoutMatrix{}, fmt.Errorf("Invalid job_layout '%s'. Expected parallel, split, or single.", layout)
	}
}
