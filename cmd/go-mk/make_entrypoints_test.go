package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

func TestMakeEntryPoints(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := os.ReadFile(filepath.Join(repoRoot, "templates", "Makefile.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := template.New("Makefile").Delims("[[", "]]").Option("missingkey=error").Parse(string(canonical))
	if err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(t.TempDir(), "go-mk")
	build := exec.Command("go", "build", "-o", engine, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build go-mk: %v\n%s", err, output)
	}
	for _, layout := range []string{"binary", "library"} {
		for _, source := range []string{"canonical", "embedded"} {
			t.Run(layout+"/"+source, func(t *testing.T) {
				consumer := t.TempDir()
				if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte("module example.com/probe\n\ngo 1.26.4\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				context := struct {
					Layout string
					Binary string
					Cmd    string
					Vpkg   string
				}{
					Layout: layout,
					Binary: "probe",
					Cmd:    "./cmd/probe",
				}
				if source == "embedded" {
					command := exec.Command(engine, "scaffold", "--"+layout, "--yes")
					command.Dir = consumer
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("scaffold failed: %v\n%s", err, output)
					}
				} else {
					var makefile bytes.Buffer
					if err := renderer.Execute(&makefile, context); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(consumer, "Makefile"), makefile.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
					bootstrap, err := os.ReadFile(filepath.Join(repoRoot, "bootstrap.mk"))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(consumer, "bootstrap.mk"), bootstrap, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				for _, scenario := range []struct {
					name      string
					variables []string
				}{
					{name: "lint"},
					{name: "workspace", variables: []string{"GO_MK_WORKSPACE_USE=."}},
					{name: "generate", variables: []string{"GO_MK_GENERATE=generate-required"}},
					{name: "cgo", variables: []string{"GO_MK_CGO_DEPS=required"}},
				} {
					t.Run(scenario.name, func(t *testing.T) {
						arguments := append([]string{"check"}, scenario.variables...)
						command := exec.Command("make", arguments...)
						command.Dir = consumer
						command.Env = append(os.Environ(), "GO_MK_DEV_DIR="+repoRoot, "GOWORK=off", "MAKEFLAGS=")
						output, err := command.CombinedOutput()
						if err == nil {
							t.Fatalf("make check succeeded: %s", output)
						}
						if !strings.Contains(string(output), "make check is not supported. Run make build directly.") {
							t.Fatalf("make check did not print build guidance: %v\n%s", err, output)
						}
						if strings.Contains(string(output), "lint-golangci") {
							t.Fatalf("make check ran lint: %s", output)
						}
						if _, err := os.Stat(filepath.Join(consumer, "go.work")); !os.IsNotExist(err) {
							t.Fatalf("make check created go.work or failed to inspect it: %v", err)
						}
					})
				}
				command := exec.Command("make")
				command.Dir = consumer
				command.Env = append(os.Environ(), "GO_MK_DEV_DIR="+repoRoot, "GOWORK=off", "MAKEFLAGS=")
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("bare make failed: %v\n%s", err, output)
				}
				if !strings.Contains(string(output), "Canonical entry points:") {
					t.Fatalf("bare make did not print help: %s", output)
				}
			})
		}
	}
}
