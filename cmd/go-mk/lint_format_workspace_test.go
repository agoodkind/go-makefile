package main_test

import (
	"os"
	"path/filepath"
	"testing"
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
