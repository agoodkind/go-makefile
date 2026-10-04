package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testHabitFlags = "STATICCHECK_EXTRA_TEST_FLAGS=-testpackage -testsourcefile -testassert -testdouble"

	olderTestBaselineRow = "widget/widget_test.go:1:9: This test file declares package widget, the package under test. A test must enter through the exported API. Change the package clause to widget_test and call only exported identifiers.\t# staticcheck-extra:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z\n"

	assertionFreeTest = "package widget_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/consumer/widget\"\n)\n\nfunc TestDoubleRuns(t *testing.T) {\n\twidget.Double(2)\n}\n"
	assertingTest     = "package widget_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/consumer/widget\"\n)\n\nfunc TestDoubleOfThree(t *testing.T) {\n\tif widget.Double(3) != 6 {\n\t\tt.Fatal(\"Double(3) != 6\")\n\t}\n}\n"
)

// TestStaticcheckExtraGatesTestHabitFindings enters through the make target a
// consumer runs. The consumer has one test file in the package under test.
func TestStaticcheckExtraGatesTestHabitFindings(t *testing.T) {
	consumerDir := newConsumer(t)

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("gate failed with the test-habit analyzers off: %v\n%s", err, output)
	}

	output, err = runConsumerMake(consumerDir, "staticcheck-extra", testHabitFlags)
	if err == nil {
		t.Fatalf("gate passed on a test in the package under test with no baseline:\n%s", output)
	}
	if !strings.Contains(output, "widget/widget_test.go:1:9") {
		t.Fatalf("failure output lacks the test finding:\n%s", output)
	}

	writeConsumerFile(t, consumerDir, ".staticcheck-extra-baseline.txt", olderTestBaselineRow)
	output, err = runConsumerMake(consumerDir, "staticcheck-extra", testHabitFlags)
	if err != nil {
		t.Fatalf("gate failed on the baselined older test: %v\n%s", err, output)
	}

	writeConsumerFile(t, consumerDir, "widget/useful_test.go", assertingTest)
	output, err = runConsumerMake(consumerDir, "staticcheck-extra", testHabitFlags)
	if err != nil {
		t.Fatalf("gate failed on a new external test with an assertion: %v\n%s", err, output)
	}

	writeConsumerFile(t, consumerDir, "widget/cruft_test.go", assertionFreeTest)
	output, err = runConsumerMake(consumerDir, "staticcheck-extra", testHabitFlags)
	if err == nil {
		t.Fatalf("gate passed with a new test that asserts nothing:\n%s", output)
	}
	if !strings.Contains(output, "widget/cruft_test.go:9:6") {
		t.Fatalf("failure output lacks the new test finding:\n%s", output)
	}
	if strings.Contains(output, "widget/widget_test.go:1:9") {
		t.Fatalf("failure output reports the baselined older test:\n%s", output)
	}
}

// newConsumer creates a consumer module that includes go.mk from the checkout
// under test.
func newConsumer(t *testing.T) string {
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
		"Makefile":              "GO_MK_DEV_DIR := " + repoRoot + "\n_GO_MK_PROVISIONED := 1\ninclude " + filepath.Join(repoRoot, "go.mk") + "\n",
	}
	for name, content := range files {
		writeConsumerFile(t, consumerDir, name, content)
	}
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

// runConsumerMake runs make in the consumer with an allow-listed environment.
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
