package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type jobLayoutEntry struct {
	Name                  string `json:"name"`
	Command               string `json:"command"`
	SecondaryCommand      string `json:"secondary_command"`
	ContinueOnError       bool   `json:"continue_on_error"`
	InstallGolangciLint   bool   `json:"install_golangci_lint"`
	GolangciLintCacheSlug string `json:"lint_cache_slug"`
}

type jobLayoutMatrix struct {
	Include []jobLayoutEntry `json:"include"`
}

func TestCIJobLayout(t *testing.T) {
	repoRoot := repoRootForTest(t)
	consumerDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(consumerDir, ".make"), 0o755); err != nil {
		t.Fatalf("create consumer .make: %v", err)
	}
	if err := os.WriteFile(filepath.Join(consumerDir, ".make", "golangci.yml"), []byte("version: \"2\"\n"), 0o644); err != nil {
		t.Fatalf("seed golangci configuration: %v", err)
	}
	makefile := "GO_MK_DEV_DIR := " + repoRoot + "\n" +
		"_GO_MK_PROVISIONED := 1\n" +
		"include " + filepath.Join(repoRoot, "go.mk") + "\n"
	if err := os.WriteFile(filepath.Join(consumerDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write consumer Makefile: %v", err)
	}

	testCases := []struct {
		name        string
		layout      string
		want        []jobLayoutEntry
		wantFailure string
	}{
		{
			name:   "parallel preserves nine quality jobs",
			layout: "parallel",
			want: []jobLayoutEntry{
				{Name: "Quality / Vet", Command: "make vet"},
				{Name: "Quality / Test", Command: "make test"},
				{Name: "Quality / Golangci Lint", Command: "make lint-golangci", InstallGolangciLint: true, GolangciLintCacheSlug: "golangci"},
				{Name: "Quality / Format", Command: "make lint-format", InstallGolangciLint: true},
				{Name: "Quality / Gocyclo", Command: "make lint-gocyclo"},
				{Name: "Quality / Deadcode", Command: "make lint-deadcode"},
				{Name: "Quality / Staticcheck Extra", Command: "make staticcheck-extra"},
				{Name: "Quality / Govulncheck", Command: "make govulncheck", ContinueOnError: true},
				{Name: "Quality / Go Version", Command: "make go-version-check"},
			},
		},
		{
			name:   "split separates test and quality",
			layout: "split",
			want: []jobLayoutEntry{
				{Name: "Test", Command: "make test"},
				{Name: "Quality", Command: "make build-check", InstallGolangciLint: true, GolangciLintCacheSlug: "golangci"},
			},
		},
		{
			name:   "single runs both commands",
			layout: "single",
			want: []jobLayoutEntry{
				{Name: "Checks", Command: "make build-check", SecondaryCommand: "make test", InstallGolangciLint: true, GolangciLintCacheSlug: "golangci"},
			},
		},
		{
			name:        "invalid layout fails",
			layout:      "invalid",
			wantFailure: "Invalid job_layout 'invalid'. Expected parallel, split, or single.",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			githubOutput := filepath.Join(t.TempDir(), "github-output")
			command := exec.Command("make", "go-mk-ci-job-layout")
			command.Dir = consumerDir
			command.Env = testProcessEnvironment(map[string]string{
				"GITHUB_OUTPUT":    githubOutput,
				"GO_MK_JOB_LAYOUT": testCase.layout,
			})
			output, err := command.CombinedOutput()
			if testCase.wantFailure != "" {
				if err == nil {
					t.Fatalf("make CI job layout succeeded, want failure\noutput:\n%s", output)
				}
				if !strings.Contains(string(output), testCase.wantFailure) {
					t.Fatalf("make CI job layout output = %q, want error containing %q", output, testCase.wantFailure)
				}
				return
			}
			if err != nil {
				t.Fatalf("make CI job layout failed: %v\noutput:\n%s", err, output)
			}

			contents, err := os.ReadFile(githubOutput)
			if err != nil {
				t.Fatalf("read CI job layout output: %v", err)
			}
			matrixJSON := strings.TrimPrefix(strings.TrimSpace(string(contents)), "matrix=")
			var matrix jobLayoutMatrix
			if err := json.Unmarshal([]byte(matrixJSON), &matrix); err != nil {
				t.Fatalf("decode CI job layout matrix %q: %v", matrixJSON, err)
			}
			if len(matrix.Include) != len(testCase.want) {
				t.Fatalf("CI job count = %d, want %d: %#v", len(matrix.Include), len(testCase.want), matrix.Include)
			}
			for index, want := range testCase.want {
				if matrix.Include[index] != want {
					t.Fatalf("CI job %d = %#v, want %#v", index, matrix.Include[index], want)
				}
			}
		})
	}
}
