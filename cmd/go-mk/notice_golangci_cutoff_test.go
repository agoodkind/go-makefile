package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

func TestGolangciNoticeUsesOnlyValidHistoricalFindings(t *testing.T) {
	engine := gomk.BuiltTestEngine(t)
	for _, scenario := range []struct {
		name       string
		historical bool
		current    bool
	}{
		{"later file-length finding", false, false},
		{"historical test compile error", true, false},
		{"current test compile error", false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			consumerDir := newConsumerRepository(t, beforeNoticeDate)
			writeConsumerFile(t, consumerDir, appliedNoticeFile, "")
			if scenario.historical {
				writeConsumerFile(t, consumerDir, "widget/broken_test.go", "package widget\n\nfunc Broken( {\n")
			}
			runConsumerGit(t, consumerDir, beforeNoticeDate, "add", ".")
			runConsumerGit(t, consumerDir, beforeNoticeDate, "commit", "--quiet", "--message", "Prepare historical notice source")
			if scenario.historical {
				writeConsumerFile(t, consumerDir, "widget/broken_test.go", "package widget\n")
			} else if scenario.current {
				writeConsumerFile(t, consumerDir, "widget/broken_test.go", "package widget\n\nfunc Broken( {\n")
			} else {
				writeConsumerFile(t, consumerDir, "widget/later.go", "package widget\n"+strings.Repeat("// Additional source line.\n", 1001))
			}
			runConsumerGit(t, consumerDir, afterNoticeDate, "add", ".")
			runConsumerGit(t, consumerDir, afterNoticeDate, "commit", "--quiet", "--message", "Update source after the effective date")
			const configuration = "version: \"2\"\nlinters:\n  default: none\n  enable: [revive]\n  settings:\n    revive:\n      rules:\n        - name: file-length-limit\n          arguments:\n            - max: 1000\n"
			writeConsumerFile(t, consumerDir, ".make/golangci.yml", configuration)
			const previousBaseline = "# Existing baseline.\n"
			if scenario.current {
				writeConsumerFile(t, consumerDir, ".golangci-lint-baseline.txt", previousBaseline)
			}
			writeConsumerFile(t, consumerDir, "notices.txt", "1\tINTRODUCED=2026-05-25T19:38:46-07:00 GATE=golangci LINTER=revive RULE=file-length-limit\tEnable the file length rule\n")
			command := exec.Command(engine, "notice")
			command.Dir = consumerDir
			command.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"HOME=" + os.Getenv("HOME"),
				"TMPDIR=" + os.Getenv("TMPDIR"),
				"_GO_MK_ROOT=" + consumerDir,
				"_GO_MK_NOTICES_FILE=" + filepath.Join(consumerDir, "notices.txt"),
				"GOLANGCI_LINT_FLAGS=-c .make/golangci.yml",
				"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml",
				"GOLANGCI_LINT_TARGETS=./...",
			}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("notice command failed: %v\n%s", err, output)
			}
			if scenario.historical || scenario.current {
				if !strings.Contains(string(output), "auto-baseline failed") {
					t.Fatalf("notice did not report the compiler error:\n%s", output)
				}
				if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != "" {
					t.Fatalf("notice recorded success after a compiler error: %q\n%s", applied, output)
				}
				if scenario.current {
					if baseline := readConsumerFile(t, consumerDir, ".golangci-lint-baseline.txt"); baseline != previousBaseline {
						t.Fatalf("notice changed the baseline after a current compiler error:\n%s", baseline)
					}
					return
				}
				if _, err := os.Stat(filepath.Join(consumerDir, ".golangci-lint-baseline.txt")); !os.IsNotExist(err) {
					t.Fatalf("baseline stat error = %v after a failed historical capture", err)
				}
				return
			}
			if baseline := readConsumerFile(t, consumerDir, ".golangci-lint-baseline.txt"); strings.Contains(baseline, "later.go") {
				t.Fatalf("notice accepted a file-length finding from after its effective date:\n%s", baseline)
			}
			gateOutput, gateErr := runConsumerMake(consumerDir, "lint-golangci")
			if gateErr == nil || !strings.Contains(gateOutput, "file-length-limit") {
				t.Fatalf("golangci gate did not reject the later finding: %v\n%s", gateErr, gateOutput)
			}
		})
	}
}
