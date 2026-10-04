package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	olderTestFinding  = "widget/widget_test.go:1:9"
	baselineFileName  = ".staticcheck-extra-baseline.txt"
	appliedNoticeFile = ".go-mk-applied-notices"

	// Notice 2 in notices.txt has the date 2026-10-05.
	beforeNoticeDate = "2026-01-01T00:00:00Z"
	afterNoticeDate  = "2027-01-01T00:00:00Z"

	assertionFreeTest = "package widget_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/consumer/widget\"\n)\n\nfunc TestDoubleRuns(t *testing.T) {\n\twidget.Double(2)\n}\n"
	assertingTest     = "package widget_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/consumer/widget\"\n)\n\nfunc TestDoubleOfThree(t *testing.T) {\n\tif widget.Double(3) != 6 {\n\t\tt.Fatal(\"Double(3) != 6\")\n\t}\n}\n"
)

func TestStaticcheckExtraBaselinesOlderTestFindingsOnce(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("first gate run failed in a consumer older than the notice: %v\n%s", err, output)
	}
	if baseline := readConsumerFile(t, consumerDir, baselineFileName); !strings.Contains(baseline, olderTestFinding) {
		t.Fatalf("baseline lacks the older test finding %s:\n%s", olderTestFinding, baseline)
	}
	if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != "1\n2\n" {
		t.Fatalf("applied notices = %q, want notices 1 and 2", applied)
	}

	writeConsumerFile(t, consumerDir, "widget/useful_test.go", assertingTest)
	output, err = runConsumerMake(consumerDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("gate failed on a new external test with an assertion: %v\n%s", err, output)
	}

	writeConsumerFile(t, consumerDir, "widget/cruft_test.go", assertionFreeTest)
	output, err = runConsumerMake(consumerDir, "staticcheck-extra")
	if err == nil {
		t.Fatalf("gate passed with a new test that asserts nothing:\n%s", output)
	}
	if !strings.Contains(output, "widget/cruft_test.go:9:6") {
		t.Fatalf("failure output lacks the new test finding:\n%s", output)
	}
	if strings.Contains(output, olderTestFinding) {
		t.Fatalf("failure output reports the baselined older test:\n%s", output)
	}
	if baseline := readConsumerFile(t, consumerDir, baselineFileName); strings.Contains(baseline, "cruft_test.go") {
		t.Fatalf("a later gate run baselined the new test finding:\n%s", baseline)
	}
}

func TestStaticcheckExtraDoesNotBaselineAConsumerNewerThanTheNotice(t *testing.T) {
	consumerDir := newConsumerRepository(t, afterNoticeDate)

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err == nil {
		t.Fatalf("gate passed on a test in the package under test with no baseline:\n%s", output)
	}
	if !strings.Contains(output, olderTestFinding) {
		t.Fatalf("failure output lacks the test finding:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(consumerDir, baselineFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("baseline stat error = %v, want no baseline file", statErr)
	}
}

func newConsumerRepository(t *testing.T, commitDate string) string {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	consumerDir := t.TempDir()
	files := map[string]string{
		"go.mod":                "module example.com/consumer\n\ngo 1.26\n",
		"widget/widget.go":      "package widget\n\nfunc Double(value int) int {\n\treturn value * 2\n}\n",
		"widget/widget_test.go": "package widget\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"Double(2) != 4\")\n\t}\n}\n",
		".make/golangci.yml":    "version: \"2\"\n",
		".gitignore":            ".make/\n",
		appliedNoticeFile:       "1\n",
		"Makefile":              "GO_MK_DEV_DIR := " + repoRoot + "\n_GO_MK_PROVISIONED := 1\ninclude " + filepath.Join(repoRoot, "go.mk") + "\n",
	}
	for name, content := range files {
		writeConsumerFile(t, consumerDir, name, content)
	}
	runConsumerGit(t, consumerDir, commitDate, "init", "--quiet", "--initial-branch=main")
	runConsumerGit(t, consumerDir, commitDate, "add", ".")
	runConsumerGit(t, consumerDir, commitDate, "commit", "--quiet", "--message", "adopt go-makefile")
	return consumerDir
}

func writeConsumerFile(t *testing.T, consumerDir, name, content string) {
	t.Helper()

	path := filepath.Join(consumerDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func readConsumerFile(t *testing.T, consumerDir, name string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(consumerDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}

// A user git configuration can require commit signing.
func runConsumerGit(t *testing.T, consumerDir, commitDate string, arguments ...string) {
	t.Helper()

	command := exec.Command("git", arguments...)
	command.Dir = consumerDir
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=" + commitDate,
		"GIT_COMMITTER_DATE=" + commitDate,
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

// An inherited MAKEFLAGS would override the consumer Makefile variables.
func runConsumerMake(consumerDir string, arguments ...string) (string, error) {
	command := exec.Command("make", arguments...)
	command.Dir = consumerDir
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}
	output, err := command.CombinedOutput()
	return string(output), err
}
