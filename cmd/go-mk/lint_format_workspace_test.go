package main_test

import (
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestLintFormatCreatesGoWorkForAWorkspaceModule(t *testing.T) {
	consumerDir := newConsumerRepository(t, afterNoticeDate)
	writeConsumerFile(t, consumerDir, "lib/go.mod", "module example.com/lib\n\ngo 1.26\n")
	writeConsumerFile(t, consumerDir, "lib/lib.go", "package lib\n\n// Value returns one.\nfunc Value() int { return 1 }\n")
	writeConsumerFile(t, consumerDir, "widget/uses_lib.go",
		"package widget\n\nimport \"example.com/lib\"\n\n// LibValue returns lib.Value.\nfunc LibValue() int { return lib.Value() }\n")

	output, err := runConsumerMake(consumerDir, "lint-format", "GO_MK_WORKSPACE_USE=. lib")
	if err != nil {
		t.Fatalf("make lint-format: %v\n%s", err, output)
	}
	if _, statErr := os.Stat(filepath.Join(consumerDir, "go.work")); statErr != nil {
		t.Fatalf("go.work stat error = %v, want go.work created before lint-format\n%s", statErr, output)
	}
}

func TestWorkspacePrefersModuleToolchainWithoutChangingRouting(t *testing.T) {
	consumerDir := newConsumerRepository(t, afterNoticeDate)
	writeConsumerFile(t, consumerDir, "go.mod", "module example.com/consumer\n\ngo 1.26\n\ntoolchain go1.27.3\n")
	writeConsumerFile(t, consumerDir, "lib/go.mod", "module example.com/lib\n\ngo 1.26\n")
	writeConsumerFile(t, consumerDir, "go.work", "go 1.26\n\nuse (\n\t.\n\t./lib\n)\n")

	output, err := runConsumerMake(consumerDir, "go-mk-workspace", "GO_MK_WORKSPACE_USE=.")
	if err != nil {
		t.Fatalf("make go-mk-workspace: %v\n%s", err, output)
	}
	workspace, err := modfile.ParseWork("go.work", []byte(readConsumerFile(t, consumerDir, "go.work")), nil)
	if err != nil {
		t.Fatalf("parse generated workspace: %v", err)
	}
	if workspace.Go == nil || workspace.Go.Version != "1.26" {
		t.Fatalf("workspace Go minimum = %v, want 1.26", workspace.Go)
	}
	if workspace.Toolchain == nil || workspace.Toolchain.Name != "go1.27.3" {
		t.Fatalf("workspace toolchain = %v, want go1.27.3", workspace.Toolchain)
	}
	if len(workspace.Use) != 2 || workspace.Use[0].Path != "." || workspace.Use[1].Path != "./lib" {
		t.Fatalf("workspace use paths = %v, want . and ./lib", workspace.Use)
	}
	command := exec.Command("go", "env", "GOTOOLCHAIN", "GOVERSION")
	command.Dir = consumerDir
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	compilerOutput, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read workspace compiler: %v\n%s", err, compilerOutput)
	}
	settings := strings.Fields(string(compilerOutput))
	if len(settings) != 2 || settings[0] != "auto" || version.Compare(settings[1], "go1.27.3") < 0 {
		t.Fatalf("workspace compiler = %q, want automatic go1.27.3 or newer", compilerOutput)
	}
}
