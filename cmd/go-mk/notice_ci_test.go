package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticcheckExtraInGitHubActionsDoesNotApplyANotice(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)

	command := exec.Command("make", "staticcheck-extra")
	command.Dir = consumerDir
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"GITHUB_ACTIONS=true",
		"GITHUB_RUN_ID=1",
	}
	outputBytes, err := command.CombinedOutput()
	output := string(outputBytes)
	if err == nil {
		t.Fatalf("gate passed in CI on a test finding with no committed notice:\n%s", output)
	}
	if !strings.Contains(output, olderTestFinding) {
		t.Fatalf("failure output lacks the test finding:\n%s", output)
	}
	if !strings.Contains(output, "go-makefile notice #2 is not applied") {
		t.Fatalf("output lacks the unapplied notice line:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(consumerDir, baselineFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("baseline stat error = %v, want no file written in CI", statErr)
	}
	if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != "1\n" {
		t.Fatalf("applied notices = %q, want only notice 1", applied)
	}
}
