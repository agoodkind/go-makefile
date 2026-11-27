package main_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goodkind.io/go-makefile/internal/notice/history"
)

func TestHistoryRestoresDirectoryAndEnvironmentAfterFailures(t *testing.T) {
	for _, scenario := range []string{"preparation", "capture"} {
		t.Run(
			scenario,
			func(t *testing.T) {
				consumer := historyFailureConsumer(t, scenario)
				const findings = "current-findings.txt"
				const currentFinding = "fixture.go:3:1: duplicate (dupl)\n"
				writeConsumerFile(t, consumer, findings, currentFinding)

				aliases := t.TempDir()
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}

				for _, name := range []string{"golangci-lint", "staticcheck-extra"} {
					if err := os.Symlink(executable, filepath.Join(aliases, name)); err != nil {
						t.Fatal(err)
					}
				}

				t.Setenv("PATH", aliases+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("GO_MK_NOTICE_MAKEFILE", "")
				t.Setenv("_GO_MK_ROOT", consumer)
				t.Setenv("STATICCHECK_EXTRA_BIN", "staticcheck-extra")
				t.Setenv("GOLANGCI_LINT", "golangci-lint")
				t.Chdir(consumer)
				workingDirectory, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}

				introduced, err := time.Parse(time.RFC3339, "2026-11-07T14:59:16-08:00")
				if err != nil {
					t.Fatal(err)
				}

				captureFailure := errors.New("analyzer failed in the historical checkout")
				capture := func(_, _ string) error {
					directory, err := os.Getwd()
					if err != nil {
						t.Fatal(err)
					}

					if directory == workingDirectory {
						t.Error("capture ran in the current checkout")
					}

					return captureFailure
				}
				err = history.RestrictFindings(
					t.Context(),
					filepath.Join(consumer, findings),
					introduced,
					"golangci",
					"",
					historyGitOutput,
					capture,
				)
				if err == nil {
					t.Fatal("historical preparation/capture failure did not fail restriction")
				}

				if scenario == "preparation" &&
					!strings.Contains(
						err.Error(),
						"prepare source before the notice effective date",
					) {
					t.Fatalf("preparation failure changed its stage: %v", err)
				}

				if scenario == "capture" &&
					!errors.Is(err, captureFailure) {
					t.Fatalf("capture failure was not returned: %v", err)
				}

				if current, err := os.Getwd(); err != nil || current != workingDirectory {
					t.Fatalf("historical failure did not restore the current directory: %v", err)
				}

				if current := readConsumerFile(t, consumer, findings); current != currentFinding {
					t.Fatalf("historical failure changed the current findings: %q", current)
				}

				for name, expected := range map[string]string{
					"_GO_MK_ROOT":           consumer,
					"STATICCHECK_EXTRA_BIN": "staticcheck-extra",
					"GOLANGCI_LINT":         "golangci-lint",
				} {
					if value, exists := os.LookupEnv(name); !exists || value != expected {
						t.Fatalf("historical failure did not restore %s", name)
					}
				}
			},
		)
	}
}

func historyGitOutput(_ string, arguments ...string) (string, error) {
	command := exec.CommandContext(context.Background(), "git", arguments...)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("query real historical Git source: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}
