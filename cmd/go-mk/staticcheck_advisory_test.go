package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const advisoryFindingLocation = "widget/widget_test.go:1:9:"

// TestStaticcheckExtraAdvisoryFindings enters through the make target a
// consumer runs. The consumer has one test file in the package under test and
// no gated finding.
func TestStaticcheckExtraAdvisoryFindings(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	t.Run("default flags print the finding and pass the gate", func(t *testing.T) {
		consumerDir := writeAdvisoryConsumer(t, repoRoot)
		output, err := runConsumerMake(consumerDir, "staticcheck-extra")
		if err != nil {
			t.Fatalf("make staticcheck-extra failed, want an advisory finding to pass the gate: %v\n%s", err, output)
		}
		if !strings.Contains(output, advisoryFindingLocation) {
			t.Fatalf("output lacks the advisory finding at %s:\n%s", advisoryFindingLocation, output)
		}
		findingsFile := filepath.Join(consumerDir, ".make", "staticcheck-extra-advisory.out")
		recorded, err := os.ReadFile(findingsFile)
		if err != nil {
			t.Fatalf("read advisory findings file: %v", err)
		}
		if !strings.HasPrefix(string(recorded), advisoryFindingLocation) {
			t.Fatalf("advisory findings file = %q, want a line at %s", recorded, advisoryFindingLocation)
		}
	})

	t.Run("an empty flag list runs no advisory analyzer", func(t *testing.T) {
		consumerDir := writeAdvisoryConsumer(t, repoRoot)
		output, err := runConsumerMake(consumerDir, "staticcheck-extra", "STATICCHECK_EXTRA_ADVISORY_FLAGS=")
		if err != nil {
			t.Fatalf("make staticcheck-extra failed: %v\n%s", err, output)
		}
		if strings.Contains(output, "advisory") {
			t.Fatalf("output reports advisory findings with an empty flag list:\n%s", output)
		}
	})
}

const (
	assertionFreeTest = "package widget_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/consumer/widget\"\n)\n\nfunc TestDoubleRuns(t *testing.T) {\n\twidget.Double(2)\n}\n"
	logOnlyTest       = "package widget_test\n\nimport \"testing\"\n\nfunc TestFeatureLogsOnly(t *testing.T) {\n\tt.Log(\"the feature test asserts nothing\")\n\tt.Log(\"a second log line\")\n}\n"
	assertingTest     ="package widget_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/consumer/widget\"\n)\n\nfunc TestDoubleOfThree(t *testing.T) {\n\tif widget.Double(3) != 6 {\n\t\tt.Fatal(\"Double(3) != 6\")\n\t}\n}\n"
)

// TestStaticcheckExtraBlocksNewTestCode enters through the make target a
// consumer runs. The consumer is a git repository with one committed test file
// in the package under test. That file predates the base commit.
func TestStaticcheckExtraBlocksNewTestCode(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	consumerDir := writeAdvisoryConsumer(t, repoRoot)
	writeConsumerFile(t, consumerDir, ".gitignore", ".make/\n")
	runConsumerGit(t, consumerDir, "init", "--quiet", "--initial-branch=main")
	runConsumerGit(t, consumerDir, "add", ".")
	runConsumerGit(t, consumerDir, "commit", "--quiet", "--message", "base")
	// The remote-tracking reference stands in for a fetched default branch.
	runConsumerGit(t, consumerDir, "update-ref", "refs/remotes/origin/main", "HEAD")

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("gate failed on test code older than the base commit: %v\n%s", err, output)
	}
	if !strings.Contains(output, "staticcheck-extra: 1 advisory finding (not gated)") {
		t.Fatalf("output lacks the advisory finding for the older test file:\n%s", output)
	}

	writeConsumerFile(t, consumerDir, "widget/useful_test.go", assertingTest)
	output, err = runConsumerMake(consumerDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("gate failed on a new external test with an assertion: %v\n%s", err, output)
	}

	writeConsumerFile(t, consumerDir, "widget/committed_test.go", assertionFreeTest)
	runConsumerGit(t, consumerDir, "add", "widget/committed_test.go")
	runConsumerGit(t, consumerDir, "commit", "--quiet", "--message", "add test")
	writeConsumerFile(t, consumerDir, "widget/untracked_test.go",
		strings.Replace(assertionFreeTest, "TestDoubleRuns", "TestDoubleRunsAgain", 1))
	output, err = runConsumerMake(consumerDir, "staticcheck-extra")
	if err == nil {
		t.Fatalf("gate passed with two new tests that assert nothing:\n%s", output)
	}
	wantLines := []string{
		"staticcheck-extra: FAILED",
		"Findings in new test code: 2",
		"widget/committed_test.go:9:6:",
		"widget/untracked_test.go:9:6:",
	}
	for _, want := range wantLines {
		if !strings.Contains(output, want) {
			t.Fatalf("failure output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, advisoryFindingLocation) {
		t.Fatalf("failure output blocks the test file older than the base commit:\n%s", output)
	}

	output, err = runConsumerMake(consumerDir, "staticcheck-extra", "STATICCHECK_EXTRA_TEST_BLOCK=off")
	if err != nil {
		t.Fatalf("gate failed with blocking turned off: %v\n%s", err, output)
	}
	if !strings.Contains(output, "staticcheck-extra: 3 advisory findings (not gated)") {
		t.Fatalf("output lacks the three advisory findings with blocking turned off:\n%s", output)
	}
}

// TestStaticcheckExtraBlocksNewTestCodeInShallowActionsCheckout enters through
// the make target in a depth-one clone with the GitHub Actions environment. The
// clone has no default branch reference and no history.
func TestStaticcheckExtraBlocksNewTestCodeInShallowActionsCheckout(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	originDir := writeAdvisoryConsumer(t, repoRoot)
	writeConsumerFile(t, originDir, ".gitignore", ".make/\n")
	runConsumerGit(t, originDir, "init", "--quiet", "--initial-branch=main")
	runConsumerGit(t, originDir, "config", "uploadpack.allowAnySHA1InWant", "true")
	runConsumerGit(t, originDir, "add", ".")
	runConsumerGit(t, originDir, "commit", "--quiet", "--message", "base")
	beforeCommit := consumerGitOutput(t, originDir, "rev-parse", "HEAD")
	runConsumerGit(t, originDir, "branch", "feature")
	writeConsumerFile(t, originDir, "widget/pushed_test.go", assertionFreeTest)
	runConsumerGit(t, originDir, "add", "widget/pushed_test.go")
	runConsumerGit(t, originDir, "commit", "--quiet", "--message", "push to main")
	runConsumerGit(t, originDir, "checkout", "--quiet", "feature")
	// The feature file differs from the file pushed to main. Git would report a
	// file with the same content as a rename of the pushed file, with no added
	// line.
	writeConsumerFile(t, originDir, "widget/feature_test.go", logOnlyTest)
	runConsumerGit(t, originDir, "add", "widget/feature_test.go")
	runConsumerGit(t, originDir, "commit", "--quiet", "--message", "feature work")

	eventPath := filepath.Join(t.TempDir(), "event.json")
	event := `{"before":"` + beforeCommit + `","repository":{"default_branch":"main"}}`
	if err := os.WriteFile(eventPath, []byte(event), 0o644); err != nil {
		t.Fatalf("write event payload: %v", err)
	}

	cases := []struct {
		name        string
		branch      string
		wantBlocked string
		wantAbsent  string
	}{
		{name: "feature branch", branch: "feature", wantBlocked: "widget/feature_test.go:5:6:", wantAbsent: "widget/pushed_test.go"},
		{name: "push to the default branch", branch: "main", wantBlocked: "widget/pushed_test.go:9:6:", wantAbsent: "widget/feature_test.go"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cloneDir := filepath.Join(t.TempDir(), "clone")
			runConsumerGit(t, originDir, "clone", "--quiet", "--depth=1", "--branch", testCase.branch,
				"file://"+originDir, cloneDir)
			writeConsumerFile(t, cloneDir, ".make/golangci.yml", "version: \"2\"\n")

			output, err := runConsumerMakeWithEnv(cloneDir, []string{
				"GITHUB_ACTIONS=true",
				"GITHUB_EVENT_PATH=" + eventPath,
				"GITHUB_REF_NAME=" + testCase.branch,
			}, "staticcheck-extra")
			if err == nil {
				t.Fatalf("gate passed in a shallow checkout with a new test that asserts nothing:\n%s", output)
			}
			if !strings.Contains(output, "Findings in new test code: 1") {
				t.Fatalf("failure output lacks the single blocked finding:\n%s", output)
			}
			if !strings.Contains(output, testCase.wantBlocked) {
				t.Fatalf("failure output lacks %s:\n%s", testCase.wantBlocked, output)
			}
			if strings.Contains(output, testCase.wantAbsent) || strings.Contains(output, advisoryFindingLocation) {
				t.Fatalf("failure output blocks a file that this ref did not add:\n%s", output)
			}
		})
	}
}

func consumerGitOutput(t *testing.T, consumerDir string, arguments ...string) string {
	t.Helper()

	command := exec.Command("git", arguments...)
	command.Dir = consumerDir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output))
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

// runConsumerGit runs git in the consumer without the user and system
// configuration. A user configuration can require commit signing.
func runConsumerGit(t *testing.T, consumerDir string, arguments ...string) {
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
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

// writeAdvisoryConsumer creates a consumer module that includes go.mk from the
// checkout under test.
func writeAdvisoryConsumer(t *testing.T, repoRoot string) string {
	t.Helper()

	consumerDir := t.TempDir()
	files := map[string]string{
		"go.mod":                "module example.com/consumer\n\ngo 1.26\n",
		"widget/widget.go":      "package widget\n\nfunc Double(value int) int {\n\treturn value * 2\n}\n",
		"widget/widget_test.go": "package widget\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"Double(2) != 4\")\n\t}\n}\n",
		".make/golangci.yml":    "version: \"2\"\n",
		"Makefile":              "GO_MK_DEV_DIR := " + repoRoot + "\n_GO_MK_PROVISIONED := 1\ninclude " + filepath.Join(repoRoot, "go.mk") + "\n",
	}
	for name, content := range files {
		writeConsumerFile(t, consumerDir, name, content)
	}
	return consumerDir
}

// runConsumerMake runs make in the consumer with an allow-listed environment.
// An inherited MAKEFLAGS would override the consumer Makefile variables.
func runConsumerMake(consumerDir string, arguments ...string) (string, error) {
	return runConsumerMakeWithEnv(consumerDir, nil, arguments...)
}

func runConsumerMakeWithEnv(consumerDir string, extraEnv []string, arguments ...string) (string, error) {
	command := exec.Command("make", arguments...)
	command.Dir = consumerDir
	command.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}, extraEnv...)
	output, err := command.CombinedOutput()
	return string(output), err
}
