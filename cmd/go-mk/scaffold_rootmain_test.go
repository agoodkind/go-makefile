package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const rootModule = "module example.com/rootmain\n\ngo 1.26\n"

func TestScaffoldTreatsARootMainPackageAsABinary(t *testing.T) {
	output, makefile := scaffoldModule(t, map[string]string{
		"go.mod":  rootModule,
		"main.go": "package main\n\nfunc main() {}\n",
	})
	if !strings.Contains(output, "layout:  binary") {
		t.Fatalf("scaffold output lacks the binary layout:\n%s", output)
	}
	for _, want := range []string{"BINARY := rootmain\n", "CMD    := .\n"} {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile lacks %q:\n%s", want, makefile)
		}
	}
}

func TestScaffoldTreatsALibraryWithAnIgnoredGeneratorAsALibrary(t *testing.T) {
	output, makefile := scaffoldModule(t, map[string]string{
		"go.mod": rootModule,
		"lib.go": "package rootmain\n\nfunc Value() int { return 1 }\n",
		"gen.go": "//go:build ignore\n\npackage main\n\nfunc main() {}\n",
	})
	if !strings.Contains(output, "layout:  library") {
		t.Fatalf("scaffold output lacks the library layout:\n%s", output)
	}
	if !strings.Contains(makefile, "LIBRARY := 1\n") {
		t.Fatalf("Makefile lacks the library setting:\n%s", makefile)
	}
}

func scaffoldModule(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	engine := filepath.Join(t.TempDir(), "go-mk")
	build := exec.Command("go", "build", "-o", engine, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build go-mk: %v\n%s", err, output)
	}

	repoDir := filepath.Join(t.TempDir(), "rootmain")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create %s: %v", repoDir, err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	scaffold := exec.Command(engine, "scaffold", "--yes")
	scaffold.Dir = repoDir
	scaffold.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}
	output, err := scaffold.CombinedOutput()
	if err != nil {
		t.Fatalf("go-mk scaffold: %v\n%s", err, output)
	}
	makefile, err := os.ReadFile(filepath.Join(repoDir, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	return string(output), string(makefile)
}
