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
		if !strings.Contains(output, "staticcheck-extra: 1 advisory finding (not gated)") {
			t.Fatalf("output lacks the advisory count line:\n%s", output)
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

// writeAdvisoryConsumer creates a consumer module that includes go.mk from the
// checkout under test.
func writeAdvisoryConsumer(t *testing.T, repoRoot string) string {
	t.Helper()

	consumerDir := t.TempDir()
	files := map[string]string{
		"go.mod":                 "module example.com/consumer\n\ngo 1.26\n",
		"widget/widget.go":       "package widget\n\nfunc Double(value int) int {\n\treturn value * 2\n}\n",
		"widget/widget_test.go":  "package widget\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"Double(2) != 4\")\n\t}\n}\n",
		".make/golangci.yml":     "version: \"2\"\n",
		"Makefile":               "GO_MK_DEV_DIR := " + repoRoot + "\n_GO_MK_PROVISIONED := 1\ninclude " + filepath.Join(repoRoot, "go.mk") + "\n",
		".make/scripts/.keep":    "",
		".make/notices.txt.keep": "",
	}
	for name, content := range files {
		path := filepath.Join(consumerDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return consumerDir
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
