package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

const multilineGate = "lint-golangci"

const multilineLoopSource = `package widget_test

import "testing"

func loopLimit() int { return 3 }

func TestIntegerLoop(t *testing.T) {
	total := 0
	for index := 0; index < loopLimit(); index++ {
		total += index
	}
	if total != 3 {
		t.Fatal(total)
	}
}
`

// TestNoticeBaselinesMultilineGolangciDiagnostics checks historical and later
// findings through the real linter.
func TestNoticeBaselinesMultilineGolangciDiagnostics(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	engine := gomk.BuiltTestEngine(t)

	linter, err := exec.LookPath("golangci-lint")
	if err != nil {
		t.Fatalf("locate golangci-lint: %v", err)
	}

	writeConsumerFile(t, consumer, "widget/range_test.go", multilineLoopSource)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical integer loop")

	const configuration = "version: \"2\"\nrun:\n  relative-path-mode: wd\n" +
		"linters:\n  default: none\n  enable: [intrange]\n"

	const notice = "13\tINTRODUCED=2026-11-07T15:17:21-08:00 GATE=golangci LINTER=intrange\t" +
		"Enable integer range checks\n"

	writeConsumerFile(t, consumer, ".make/golangci.yml", configuration)
	writeConsumerFile(t, consumer, "notices.txt", notice)
	noticesPath := filepath.Join(consumer, "notices.txt")
	command := exec.CommandContext(t.Context(), engine, "notice")
	command.Dir = consumer
	command.Env = append(
		os.Environ(),
		"_GO_MK_ROOT="+consumer,
		"_GO_MK_NOTICES_FILE="+noticesPath,
		"GOLANGCI_LINT="+linter,
		"GOLANGCI_LINT_FLAGS=-c .make/golangci.yml",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml",
		"GOLANGCI_LINT_TARGETS=./widget",
	)
	output, err := command.CombinedOutput()

	if err != nil || strings.Contains(string(output), "auto-baseline failed") {
		t.Fatalf("multiline notice failed: %v\n%s", err, output)
	}

	if applied := readConsumerFile(t, consumer, appliedNoticeFile); applied != "1\n13\n" {
		t.Fatalf("applied notices = %q, want the integer range notice:\n%s", applied, output)
	}

	const explanation = "take care to consider side effects. (intrange)"

	baseline := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt")
	complete := false

	for line := range strings.SplitSeq(baseline, "\n") {
		located := strings.HasPrefix(line, "widget/range_test.go:")
		header := strings.Contains(line, "for loop can be changed to use an integer range")
		continued := strings.Contains(line, explanation)

		if located && header && continued {
			complete = true
		}
	}

	if !complete {
		t.Fatalf("baseline omitted the located multiline diagnostic:\n%s", baseline)
	}

	arguments := []string{
		multilineGate,
		"GO_MK_BIN=" + engine,
		"GOLANGCI_LINT=" + linter,
		"GOLANGCI_LINT_FLAGS=-c .make/golangci.yml",
		"GOLANGCI_LINT_TARGETS=./widget",
		"_GO_MK_NOTICES_FILE=" + noticesPath,
	}

	gateOutput, gateError := runConsumerMake(consumer, arguments...)
	if gateError != nil {
		t.Fatalf("historical multiline finding failed its gate: %v\n%s", gateError, gateOutput)
	}

	later := strings.ReplaceAll(multilineLoopSource, "loopLimit", "laterLimit")
	later = strings.ReplaceAll(later, "TestIntegerLoop", "TestLaterIntegerLoop")
	writeConsumerFile(t, consumer, "widget/later_test.go", later)
	gateOutput, gateError = runConsumerMake(consumer, arguments...)
	reportsLater := strings.Contains(gateOutput, "widget/later_test.go:")
	reportsExplanation := strings.Contains(gateOutput, explanation)

	if gateError == nil || !reportsLater || !reportsExplanation {
		t.Fatalf("gate accepted the later multiline finding: %v\n%s", gateError, gateOutput)
	}

	if current := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt"); current != baseline {
		t.Fatalf("gate changed the historical baseline:\n%s", current)
	}
}
