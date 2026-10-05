package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldTreatsARootMainPackageAsABinary(t *testing.T) {
	engine := filepath.Join(t.TempDir(), "go-mk")
	build := exec.Command("go", "build", "-o", engine, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build go-mk: %v\n%s", err, output)
	}

	repoDir := filepath.Join(t.TempDir(), "rootmain")
	files := map[string]string{
		"go.mod":  "module example.com/rootmain\n\ngo 1.26\n",
		"main.go": "package main\n\nfunc main() {}\n",
	}
	for name, content := range files {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
	if !strings.Contains(string(output), "layout:  binary") {
		t.Fatalf("scaffold output lacks the binary layout:\n%s", output)
	}

	makefile, err := os.ReadFile(filepath.Join(repoDir, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	for _, want := range []string{"BINARY := rootmain\n", "CMD    := .\n"} {
		if !strings.Contains(string(makefile), want) {
			t.Fatalf("Makefile lacks %q:\n%s", want, makefile)
		}
	}
}
