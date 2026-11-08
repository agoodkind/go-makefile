package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

const (
	testHabitNoticeIDs = "1\n2\n3\n"
	olderTestFinding   = "widget/widget_test.go:1:9"
	baselineFileName   = ".staticcheck-extra-baseline.txt"
	appliedNoticeFile  = ".go-mk-applied-notices"

	// Notice 2 in notices.txt has the date 2026-10-05.
	beforeNoticeDate = "2026-01-01T00:00:00Z"
	afterNoticeDate  = "2027-01-01T00:00:00Z"

	testPreamble = `package widget_test

import (
	"testing"

	"example.com/consumer/widget"
)

`
	assertionFreeTest = testPreamble + `func TestDoubleRuns(t *testing.T) {
	widget.Double(2)
}
`
	assertingTest = testPreamble + `func TestDoubleOfThree(t *testing.T) {
	if widget.Double(3) != 6 {
		t.Fatal("Double(3) != 6")
	}
}
`
)

// TestStaticcheckExtraBaselinesOlderTestFindingsOnce checks notice adoption.
func TestStaticcheckExtraBaselinesOlderTestFindingsOnce(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("first gate run failed in a consumer older than the notice: %v\n%s", err, output)
	}

	if baseline := readConsumerFile(t, consumerDir, baselineFileName); !strings.Contains(baseline, olderTestFinding) {
		t.Fatalf("baseline lacks the older test finding %s:\n%s", olderTestFinding, baseline)
	}

	if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != testHabitNoticeIDs {
		t.Fatalf("applied notices = %q, want notices 1, 2, and 3", applied)
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

// TestStaticcheckExtraDoesNotBaselineAConsumerNewerThanTheNotice checks a
// consumer that adopted the rules after the cutoff.
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

	fixtureDir := t.TempDir()
	consumerDir := filepath.Join(fixtureDir, "checkout")
	if err := os.Mkdir(consumerDir, 0o700); err != nil {
		t.Fatalf("create consumer checkout: %v", err)
	}

	if err := os.Mkdir(filepath.Join(fixtureDir, "tmp"), 0o700); err != nil {
		t.Fatalf("create consumer temporary directory: %v", err)
	}

	files := map[string]string{
		fixtureModule: "module example.com/consumer\n\ngo 1.26\n",
		fixtureWidget: "package widget\n\nfunc Double(value int) int {\n\treturn value * 2\n}\n",
		fixtureWidgetTest: `package widget

import "testing"

func TestDouble(t *testing.T) {
	if Double(2) != 4 {
		t.Fatal("Double(2) != 4")
	}
}
`,
		".make/golangci.yml": "version: \"2\"\n",
		".gitignore":         ".make/\n",
		"notices.txt":        lintPolicyNotice(t, "1") + lintPolicyNotice(t, "2") + lintPolicyNotice(t, "3"),
		appliedNoticeFile:    "1\n",
		fixtureMakefile:      consumerMakefile(t, repoRoot),
	}
	for name, content := range files {
		writeConsumerFile(t, consumerDir, name, content)
	}

	runConsumerGit(t, consumerDir, commitDate, "init", "--quiet", "--initial-branch=main")
	runConsumerGit(t, consumerDir, commitDate, "add", ".")
	runConsumerGit(t, consumerDir, commitDate, "commit", "--quiet", "--message", "adopt go-makefile")

	return consumerDir
}

func consumerMakefile(t *testing.T, repository string) string {
	t.Helper()
	fixture, err := template.ParseFiles(filepath.Join("testdata", "consumer", fixtureMakefile))
	if err != nil {
		t.Fatal(err)
	}

	var contents strings.Builder
	if err := fixture.Execute(&contents, repository); err != nil {
		t.Fatal(err)
	}

	return contents.String()
}

func writeConsumerFile(t *testing.T, consumerDir, name, content string) {
	t.Helper()

	checkout, err := os.OpenRoot(consumerDir)
	if err != nil {
		t.Fatalf("open consumer checkout: %v", err)
	}

	defer func() { _ = checkout.Close() }()

	if err := checkout.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatalf("create directory for %s: %v", name, err)
	}

	if err := checkout.WriteFile(name, []byte(content), 0o600); err != nil {
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

	command := exec.CommandContext(t.Context(), "git", arguments...)
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
	ctx := context.Background()
	root, err := exec.CommandContext(ctx, "git", "-C", consumerDir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return string(root), err
	}

	// All modules in one consumer share its golangci-lint.lock file.
	temporary := filepath.Join(filepath.Dir(strings.TrimSpace(string(root))), "tmp")
	command := exec.CommandContext(ctx, "make", arguments...)
	command.Dir = consumerDir
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + temporary,
		"GOCACHE=" + os.Getenv("GOCACHE"),
	}
	output, err := command.CombinedOutput()

	return string(output), err
}
