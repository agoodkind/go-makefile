package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMakeRejectsConsumerRecipeReplacements(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(t.TempDir(), "go-mk")
	build := exec.Command("go", "build", "-o", engine, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build go-mk: %v\n%s", err, output)
	}
	for _, makeName := range []string{"make", "gmake"} {
		makePath, err := exec.LookPath(makeName)
		if err != nil {
			t.Logf("optional Make executable %s is unavailable: %v", makeName, err)
			continue
		}
		t.Run(makeName, func(t *testing.T) {
			for _, fixture := range []string{"after", "before", "eval", "generated", "module", "level", "origin"} {
				for _, mode := range []string{"normal", "dry-run", "question"} {
					t.Run(fixture+"/"+mode, func(t *testing.T) {
						consumer := makeContractConsumer(t, repoRoot, fixture+".mk")
						arguments := []string{"help"}
						if mode == "dry-run" {
							arguments = append([]string{"-n"}, arguments...)
						}
						if mode == "question" {
							arguments = append([]string{"-q"}, arguments...)
						}
						command := makeContractCommand(makePath, consumer, engine, repoRoot, arguments)
						output, err := command.CombinedOutput()
						assertMakeContractFailure(t, consumer, output, err)
					})
				}
			}
			t.Run("additional-makefile", func(t *testing.T) {
				consumer := makeContractConsumer(t, repoRoot, "valid.mk")
				copyMakeContractFile(t, filepath.Join("testdata", "makecontract", "extra.mk"), filepath.Join(consumer, "extra.mk"))
				command := makeContractCommand(makePath, consumer, engine, repoRoot, []string{"-f", "Makefile", "-f", "extra.mk", "MODE=enable override", "--", "help"})
				output, err := command.CombinedOutput()
				assertMakeContractFailure(t, consumer, output, err)
			})
			t.Run("changed-directory", func(t *testing.T) {
				consumer := makeContractConsumer(t, repoRoot, "after.mk")
				command := makeContractCommand(makePath, repoRoot, engine, repoRoot, []string{"-C", consumer, "help"})
				output, err := command.CombinedOutput()
				assertMakeContractFailure(t, consumer, output, err)
			})
			t.Run("separated-goals", func(t *testing.T) {
				consumer := makeContractConsumer(t, repoRoot, "after.mk")
				command := makeContractCommand(makePath, consumer, engine, repoRoot, []string{"-f", "Makefile", "--", "help"})
				output, err := command.CombinedOutput()
				assertMakeContractFailure(t, consumer, output, err)
			})
			for _, option := range []string{"-I", "-o", "-W"} {
				t.Run("option-operand-"+option, func(t *testing.T) {
					consumer := makeContractConsumer(t, repoRoot, "goal.mk")
					command := makeContractCommand(makePath, consumer, engine, repoRoot, []string{option, "-Coperand", "help"})
					output, err := command.CombinedOutput()
					assertMakeContractFailure(t, consumer, output, err)
				})
			}
			t.Run("additional-prerequisites-and-hooks", func(t *testing.T) {
				consumer := makeContractConsumer(t, repoRoot, "valid.mk")
				command := makeContractCommand(makePath, consumer, engine, repoRoot, []string{"help", "go-mk-generate", "go-mk-cgo-deps", "custom"})
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("valid consumer failed: %v\n%s", err, output)
				}
				if !strings.Contains(string(output), "Canonical entry points:") {
					t.Fatalf("valid consumer did not print shared help:\n%s", output)
				}
				for _, name := range []string{"prerequisite-ran", "generator-ran", "cgo-hook-ran", "custom-ran"} {
					if _, err := os.Stat(filepath.Join(consumer, name)); err != nil {
						t.Fatalf("valid consumer did not run %s: %v\n%s", name, err, output)
					}
				}
			})
			t.Run("verbose-engine-acquisition", func(t *testing.T) {
				consumer := makeContractConsumer(t, repoRoot, "valid.mk")
				command := exec.Command(makePath, "help")
				command.Dir = consumer
				command.Env = []string{
					"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR"),
					"GOPATH=" + os.Getenv("GOPATH"), "GO_MK_DEV_DIR=" + repoRoot, "_GO_MK_PROVISIONED=1", "GOWORK=off", "GOFLAGS=-x",
				}
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("help rejected successful engine acquisition: %v\n%s", err, output)
				}
				if !strings.Contains(string(output), "Canonical entry points:") {
					t.Fatalf("help did not print shared help after engine acquisition:\n%s", output)
				}
			})
			t.Run("failed-explicit-engine-build", func(t *testing.T) {
				consumer := makeContractConsumer(t, repoRoot, "valid.mk")
				command := exec.Command(makePath, "help")
				command.Dir = consumer
				command.Env = []string{
					"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR"),
					"GOPATH=" + os.Getenv("GOPATH"), "GO_MK_DEV_DIR=" + repoRoot, "_GO_MK_PROVISIONED=1", "GOWORK=off", "GO_MK_BUILD_PKG=./cmd/missing-contract-engine",
				}
				output, err := command.CombinedOutput()
				if err == nil || !strings.Contains(string(output), "dev build failed; correct GO_MK_BUILD_REPO") {
					t.Fatalf("Make did not report the failed explicit engine build: %v\n%s", err, output)
				}
				if _, err := os.Stat(filepath.Join(consumer, "prerequisite-ran")); !os.IsNotExist(err) {
					t.Fatalf("Make ran the consumer prerequisite after acquisition failed: %v", err)
				}
			})
		})
	}
}

func makeContractConsumer(t *testing.T, repoRoot, fixture string) string {
	t.Helper()
	consumer := t.TempDir()
	if err := os.Mkdir(filepath.Join(consumer, ".make"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mk", "go-build.mk", "golangci.yml"} {
		copyMakeContractFile(t, filepath.Join(repoRoot, name), filepath.Join(consumer, ".make", name))
	}
	copyMakeContractFile(t, filepath.Join("testdata", "makecontract", fixture), filepath.Join(consumer, "Makefile"))
	return consumer
}

func copyMakeContractFile(t *testing.T, source, destination string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeContractCommand(makePath, directory, engine, repoRoot string, arguments []string) *exec.Cmd {
	command := exec.Command(makePath, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GO_MK_DEV_DIR="+repoRoot, "GO_MK_BIN="+engine, "_GO_MK_PROVISIONED=1", "MAKEFLAGS=", "GOWORK=off")
	return command
}

func assertMakeContractFailure(t *testing.T, consumer string, output []byte, commandError error) {
	t.Helper()
	if commandError == nil {
		t.Fatalf("consumer recipe replacement succeeded:\n%s", output)
	}
	if !regexp.MustCompile(`(?i)(?:makefile|extra\.mk):[0-9]+ replaces`).Match(output) {
		t.Fatalf("consumer error omitted its source location: %v\n%s", commandError, output)
	}
	for _, expected := range []string{"shared recipe for target", "If this is a new error, please correct it now by moving the consumer recipe to a separate target and updating its callers."} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("consumer error omitted %q: %v\n%s", expected, commandError, output)
		}
	}
	for _, name := range []string{"overridden", "prerequisite-ran", "recreated.mk"} {
		if _, err := os.Stat(filepath.Join(consumer, name)); !os.IsNotExist(err) {
			t.Fatalf("recipe validation ran %s or failed to inspect it: %v\n%s", name, err, output)
		}
	}
}
