package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGolangciCacheSaveDecision(t *testing.T) {
	consumerDir := t.TempDir()
	cacheDir := filepath.Join(consumerDir, "nested", "module", ".make", "golangci-lint-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("create lint cache: %v", err)
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
			cachePath: filepath.Join(
				consumerDir,
				"missing",
				"**",
				".make",
				"golangci-lint-cache*",
			),
			wantFailure: true,
			wantError:   "no cache directory matched any configured path after make build-check",
		},
	}
	for _, testCase := range testCases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				githubOutput := filepath.Join(t.TempDir(), "github-output")
				t.Setenv("GITHUB_OUTPUT", githubOutput)
				t.Setenv("GO_MK_CI_PRIMARY_COMMAND", testCase.primaryCommand)
				t.Setenv("GO_MK_CI_PRIMARY_OUTCOME", testCase.primaryOutcome)
				t.Setenv("GO_MK_CI_GOLANGCI_RAN", testCase.golangciRan)
				t.Setenv("GO_MK_CI_LINT_CACHE_HIT", testCase.cacheHit)
				t.Setenv("GO_MK_CI_LINT_CACHE_PATHS", testCase.cachePath)

				var status int
				output := captureStderr(
					t,
					func() {
						status = runGolangciCacheSaveDecision()
					},
				)
				if testCase.wantFailure {
					if status == 0 {
						t.Fatalf("cache save decision succeeded, want failure\noutput:\n%s", output)
					}

					if !strings.Contains(string(output), testCase.wantError) {
						t.Fatalf(
							"make cache save decision output = %q, want error containing %q",
							output,
							testCase.wantError,
						)
					}

					return
				}

				if status != 0 {
					t.Fatalf(
						"cache save decision failed with status %d\noutput:\n%s",
						status,
						output,
					)
				}

				decision, err := os.ReadFile(githubOutput)
				if err != nil {
					t.Fatalf("read cache save decision: %v", err)
				}

				if string(decision) != testCase.wantOutput {
					t.Fatalf("cache save decision = %q, want %q", decision, testCase.wantOutput)
				}
			},
		)
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
