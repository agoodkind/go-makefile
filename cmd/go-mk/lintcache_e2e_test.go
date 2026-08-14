package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGolangciCacheSaveDecision enters through the make target a reusable
// workflow consumer runs and observes the GitHub step output.
func TestGolangciCacheSaveDecision(t *testing.T) {
	repoRoot := repoRootForTest(t)
	consumerDir := t.TempDir()
	cacheDir := filepath.Join(consumerDir, "nested", "module", ".make", "golangci-lint-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("create lint cache: %v", err)
	}
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
		name           string
		primaryCommand string
		primaryOutcome string
		golangciRan    string
		cacheHit       string
		cachePath      string
		wantOutput     string
		wantFailure    bool
		wantError      string
	}{
		{
			name:           "serialized failure saves warm cache",
			primaryCommand: "make build-check",
			primaryOutcome: "failure",
			golangciRan:    "true",
			cacheHit:       "false",
			cachePath:      filepath.Join(consumerDir, "**", ".make", "golangci-lint-cache*"),
			wantOutput:     "save=true\n",
		},
		{
			name:           "serialized failure before lint does not save",
			primaryCommand: "make build-check",
			primaryOutcome: "failure",
			golangciRan:    "false",
			cacheHit:       "false",
			cachePath:      cacheDir,
			wantOutput:     "save=false\n",
		},
		{
			name:           "standalone lint failure does not save",
			primaryCommand: "make lint-golangci",
			primaryOutcome: "failure",
			cacheHit:       "false",
			cachePath:      cacheDir,
			wantOutput:     "save=false\n",
		},
		{
			name:           "exact cache hit does not save",
			primaryCommand: "make build-check",
			primaryOutcome: "success",
			cacheHit:       "true",
			cachePath:      cacheDir,
			wantOutput:     "save=false\n",
		},
		{
			name:           "missing serialized cache fails",
			primaryCommand: "make build-check",
			primaryOutcome: "failure",
			golangciRan:    "true",
			cacheHit:       "false",
			cachePath:      filepath.Join(consumerDir, "missing-cache"),
			wantFailure:    true,
			wantError:      "no cache directory matched any configured path after make build-check",
		},
		{
			name:           "missing recursive root reports no cache",
			primaryCommand: "make build-check",
			primaryOutcome: "failure",
			golangciRan:    "true",
			cacheHit:       "false",
			cachePath:      filepath.Join(consumerDir, "missing", "**", ".make", "golangci-lint-cache*"),
			wantFailure:    true,
			wantError:      "no cache directory matched any configured path after make build-check",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			githubOutput := filepath.Join(t.TempDir(), "github-output")
			command := exec.Command(
				"make",
				"go-mk-golangci-cache-save-decision",
				"GO_MK_CI_PRIMARY_COMMAND="+testCase.primaryCommand,
				"GO_MK_CI_PRIMARY_OUTCOME="+testCase.primaryOutcome,
				"GO_MK_CI_GOLANGCI_RAN="+testCase.golangciRan,
				"GO_MK_CI_LINT_CACHE_HIT="+testCase.cacheHit,
				"GO_MK_CI_LINT_CACHE_PATHS="+testCase.cachePath,
			)
			command.Dir = consumerDir
			command.Env = testProcessEnvironment(map[string]string{
				"GITHUB_OUTPUT": githubOutput,
			})
			output, err := command.CombinedOutput()
			if testCase.wantFailure {
				if err == nil {
					t.Fatalf("make cache save decision succeeded, want failure\noutput:\n%s", output)
				}
				if !strings.Contains(string(output), testCase.wantError) {
					t.Fatalf("make cache save decision output = %q, want error containing %q", output, testCase.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("make cache save decision failed: %v\noutput:\n%s", err, output)
			}

			decision, err := os.ReadFile(githubOutput)
			if err != nil {
				t.Fatalf("read cache save decision: %v", err)
			}
			if string(decision) != testCase.wantOutput {
				t.Fatalf("cache save decision = %q, want %q", decision, testCase.wantOutput)
			}
		})
	}
}

func TestRepositoryMakefileForwardsGolangciCacheSaveDecision(t *testing.T) {
	repoRoot := repoRootForTest(t)
	cacheDir := filepath.Join(t.TempDir(), "golangci-lint-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("create lint cache: %v", err)
	}
	githubOutput := filepath.Join(t.TempDir(), "github-output")
	command := exec.Command(
		"make",
		"go-mk-golangci-cache-save-decision",
		"GO_MK_CI_PRIMARY_COMMAND=make build-check",
		"GO_MK_CI_PRIMARY_OUTCOME=success",
		"GO_MK_CI_LINT_CACHE_HIT=false",
		"GO_MK_CI_LINT_CACHE_PATHS="+cacheDir,
	)
	command.Dir = repoRoot
	command.Env = testProcessEnvironment(map[string]string{
		"GITHUB_OUTPUT": githubOutput,
	})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("repository Makefile cache decision failed: %v\noutput:\n%s", err, output)
	}
	decision, err := os.ReadFile(githubOutput)
	if err != nil {
		t.Fatalf("read cache save decision: %v", err)
	}
	if string(decision) != "save=true\n" {
		t.Fatalf("cache save decision = %q, want %q", decision, "save=true\n")
	}
}
