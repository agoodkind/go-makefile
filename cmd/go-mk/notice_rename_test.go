package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoticeKeepsTheAdoptionDateAfterAModuleMove(t *testing.T) {
	repositoryDir := newConsumerRepository(t, beforeNoticeDate)
	moduleEntries := []string{"go.mod", "widget", ".gitignore", appliedNoticeFile, "Makefile"}
	if err := os.MkdirAll(filepath.Join(repositoryDir, "gateway"), 0o755); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	runConsumerGit(t, repositoryDir, afterNoticeDate, append([]string{"mv"}, append(moduleEntries, "gateway")...)...)
	runConsumerGit(t, repositoryDir, afterNoticeDate, "commit", "--quiet", "--message", "move the module into gateway")
	if err := os.Rename(filepath.Join(repositoryDir, ".make"), filepath.Join(repositoryDir, "gateway", ".make")); err != nil {
		t.Fatalf("move .make: %v", err)
	}
	moduleDir := filepath.Join(repositoryDir, "gateway")

	output, err := runConsumerMake(moduleDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("gate failed in a consumer that adopted go-makefile before the notice: %v\n%s", err, output)
	}
	if baseline := readConsumerFile(t, moduleDir, baselineFileName); !strings.Contains(baseline, olderTestFinding) {
		t.Fatalf("notice output lacks %s:\n%s", olderTestFinding, baseline)
	}
}
