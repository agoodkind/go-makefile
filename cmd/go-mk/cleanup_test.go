package main_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goodkind.io/go-makefile/internal/notice/history"
)

func TestHistoryRestoresDirectoryAndEnvironmentAfterRealFailures(t *testing.T) {
	for _, scenario := range []string{"preparation", "capture"} {
		t.Run(
			scenario,
			func(t *testing.T) {
				consumer := historyFailureConsumer(t, scenario)
				output, err := runConsumerMake(
					consumer,
					"lint-golangci-scope",
					"LINTER=dupl",
					"GOLANGCI_LINT_TARGETS=./widget",
				)
				if err == nil || !strings.Contains(output, "(dupl)") {
					t.Fatalf(
						"current source did not produce real dupl findings: %v\n%s",
						err,
						output,
					)
				}

				aliases := t.TempDir()
				if err := os.Symlink(
					filepath.Join(consumer, ".make", "staticcheck-extra"),
					filepath.Join(aliases, "staticcheck-extra"),
				); err != nil {
					t.Fatal(err)
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

				capture := func(rawPath, findingsPath string) error {
					directory, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("locate actual capture directory: %w", err)
					}

					captured, err := runConsumerMake(
						directory,
						"lint-golangci",
						"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable=govet",
						"GOLANGCI_LINT_TARGETS=./widget",
					)
					if err != nil {
						return fmt.Errorf("actual capture command: %w\n%s", err, captured)
					}

					if err := copyHistoryCapture(
						directory,
						"golangci-lint.raw.out",
						rawPath,
					); err != nil {
						return err
					}

					return copyHistoryCapture(directory, "golangci-lint.out", findingsPath)
				}
				err = history.RestrictFindings(
					filepath.Join(consumer, ".make", "golangci-lint-scope.out"),
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
					(!strings.Contains(
						err.Error(),
						"capture historical notice findings",
					) || !strings.Contains(err.Error(), "missingValue")) {
					t.Fatalf("real compiler failure did not fail capture: %v", err)
				}

				if current, err := os.Getwd(); err != nil || current != workingDirectory {
					t.Fatalf("historical failure did not restore the current directory: %v", err)
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

func copyHistoryCapture(directory, name, destination string) error {
	contents, err := os.ReadFile(filepath.Join(directory, ".make", name))
	if err != nil {
		return fmt.Errorf("read actual analyzer capture: %w", err)
	}

	if err := os.WriteFile(destination, contents, 0o600); err != nil {
		return fmt.Errorf("preserve actual analyzer capture: %w", err)
	}

	return nil
}
