package main_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

// TestConfiguredAnalyzerCheckoutMustExist rejects missing and non-directory
// checkout paths before source installation.
func TestConfiguredAnalyzerCheckoutMustExist(t *testing.T) {
	t.Parallel()
	engine := gomk.BuiltTestEngine(t)

	for _, scenario := range []struct {
		name string
		file bool
	}{
		{name: "missing directory"},
		{name: "regular file", file: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			repository := filepath.Join(directory, "checkout")
			if scenario.file {
				err := os.WriteFile(repository, []byte("regular file\n"), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}

			command := exec.CommandContext(t.Context(), engine, "staticcheck-extra-bin")
			command.Dir = directory
			command.Env = append(
				noticeGitEnvironment(t),
				"_GO_MK_ROOT="+directory,
				"STATICCHECK_EXTRA_BIN=",
				"STATICCHECK_EXTRA_BUILD_REPO="+repository,
				"STATICCHECK_EXTRA_BUILD_PKG=./cmd/staticcheck-extra",
				"STATICCHECK_EXTRA_INSTALL=example.invalid/analyzer@v0.0.1",
				"GOPROXY=off",
				"GOSUMDB=off",
			)
			output, err := command.CombinedOutput()
			exitError, failed := errors.AsType[*exec.ExitError](err)
			if !failed || exitError.ExitCode() == 0 {
				t.Fatalf("invalid configured checkout did not fail: %v\n%s", err, output)
			}

			hasContext := strings.Contains(string(output), "open analyzer build repository")
			hasPath := strings.Contains(string(output), repository)
			if !hasContext || !hasPath {
				t.Fatalf("failure did not identify the required checkout:\n%s", output)
			}
		})
	}
}

// TestAnalyzerSourceOverrideAcceptsPackagePaths installs a real Go command
// from both unversioned import paths and local package paths.
func TestAnalyzerSourceOverrideAcceptsPackagePaths(t *testing.T) {
	t.Parallel()
	engine := gomk.BuiltTestEngine(t)

	for _, specification := range []string{"example.com/source/cmd/fixture", "./cmd/fixture"} {
		t.Run(specification, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			writeConsumerFile(t, directory, "go.mod", "module example.com/source\n\ngo 1.27\n")
			writeConsumerFile(t, directory, "cmd/fixture/main.go", `package main
import "fmt"
func main() { fmt.Println("installed source command") }
`)
			command := exec.CommandContext(t.Context(), engine, "staticcheck-extra-bin")
			command.Dir = directory
			command.Env = append(
				noticeGitEnvironment(t),
				"_GO_MK_ROOT="+directory,
				"STATICCHECK_EXTRA_BIN=",
				"STATICCHECK_EXTRA_BUILD_REPO=",
				"STATICCHECK_EXTRA_INSTALL="+specification,
				"STATICCHECK_EXTRA_FLAGS=",
				"STATICCHECK_EXTRA_TEST_FLAGS=",
				"GOPROXY=off",
				"GOSUMDB=off",
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("source override did not install its Go command: %v\n%s", err, output)
			}

			installed := filepath.Join(directory, ".make", "staticcheck-extra")
			run := exec.CommandContext(t.Context(), installed)
			output, err = run.CombinedOutput()
			if err != nil || string(output) != "installed source command\n" {
				t.Fatalf("installed source command did not execute: %v\n%s", err, output)
			}
		})
	}
}

// TestAnalyzerSourceOverrideRejectsInvalidInput checks source-spec validation
// before Go execution and keeps invalid input out of the error response.
func TestAnalyzerSourceOverrideRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	engine := gomk.BuiltTestEngine(t)

	for _, specification := range []string{
		"-mod=readonly",
		"example.com/tool@latest --help",
		"@latest",
		"example.com/tool@",
		"private:value@latest",
	} {
		t.Run(specification, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			command := exec.CommandContext(t.Context(), engine, "staticcheck-extra-bin")
			command.Dir = directory
			command.Env = append(
				noticeGitEnvironment(t),
				"_GO_MK_ROOT="+directory,
				"STATICCHECK_EXTRA_BIN=",
				"STATICCHECK_EXTRA_BUILD_REPO=",
				"STATICCHECK_EXTRA_INSTALL="+specification,
				"GOPROXY=off",
				"GOSUMDB=off",
			)
			output, err := command.CombinedOutput()
			_, failed := errors.AsType[*exec.ExitError](err)
			if !failed || !strings.Contains(string(output), "STATICCHECK_EXTRA_INSTALL") {
				t.Fatalf("source validation did not identify its required input: %v\n%s", err, output)
			}

			if strings.Contains(string(output), specification) {
				t.Fatalf("source validation echoed invalid input:\n%s", output)
			}
		})
	}
}
