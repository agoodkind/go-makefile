package main_test

import (
	"strings"
	"testing"
)

// TestNoticeUsesHistoricalSourceFromMergedBranch checks findings from a
// merged commit.
func TestNoticeUsesHistoricalSourceFromMergedBranch(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	setPendingNotices(t, consumer, "2")
	runConsumerGit(t, consumer, "2026-02-01T00:00:00Z", "checkout", "--quiet", "-b", "historical")
	writeConsumerFile(t, consumer, "widget/merged_test.go", assertionFreeTest)
	runConsumerGit(t, consumer, "2026-02-01T00:00:00Z", "add", ".")
	runConsumerGit(t, consumer, "2026-02-01T00:00:00Z", "commit", "--quiet", "--message", "Add historical test")
	runConsumerGit(t, consumer, afterNoticeDate, "checkout", "--quiet", "main")
	writeConsumerFile(t, consumer, "README.md", "Consumer test fixture.\n")
	runConsumerGit(t, consumer, afterNoticeDate, "add", ".")
	runConsumerGit(t, consumer, afterNoticeDate, "commit", "--quiet", "--message", "Update consumer")
	runConsumerGit(
		t,
		consumer,
		afterNoticeDate,
		"merge",
		"--quiet",
		"--no-ff",
		"--message",
		"Merge historical test",
		"historical",
	)

	output, err := runConsumerMake(consumer, fixtureStaticcheckGate)
	if err != nil {
		t.Fatalf("gate rejected a finding committed before the notice on a merged branch: %v\n%s", err, output)
	}

	baseline := readConsumerFile(t, consumer, baselineFileName)
	if !strings.Contains(baseline, "widget/merged_test.go:") {
		t.Fatalf("baseline omitted the historical merged finding:\n%s", baseline)
	}

	later := strings.ReplaceAll(assertionFreeTest, "TestDoubleRuns", "TestLaterDoubleRuns")
	writeConsumerFile(t, consumer, "widget/later_test.go", later)
	output, err = runConsumerMake(consumer, fixtureStaticcheckGate)
	if err == nil || !strings.Contains(output, "widget/later_test.go:") {
		t.Fatalf("gate accepted a finding added after notice application: %v\n%s", err, output)
	}

	if current := readConsumerFile(t, consumer, baselineFileName); current != baseline {
		t.Fatalf("later gate changed the historical baseline:\n%s", current)
	}
}

// TestNoticePreservesHistoricalIdentityAfterFormatting checks formatted
// and changed implementations.
func TestNoticePreservesHistoricalIdentityAfterFormatting(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	setPendingNotices(t, consumer)
	writeConsumerFile(t, consumer, ".make/golangci.yml", "version: \"2\"\nformatters:\n  enable:\n    - gofumpt\n")
	original := `package main
func main(){println( "historical" )}
func helper(first, second int, callback func()) { println(first, second); callback() }
`
	writeConsumerFile(t, consumer, fixtureCommand, original)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical command")

	output, err := runConsumerMake(consumer, "fmt", "GOLANGCI_FMT_FILES=cmd/tool/main.go")
	if err != nil {
		t.Fatalf("format historical source: %v\n%s", err, output)
	}

	formatted := readConsumerFile(t, consumer, fixtureCommand)
	if formatted == original {
		t.Fatal("formatter did not change the historical source layout")
	}

	previous := setPendingNotices(t, consumer, commandNotice)
	arguments := []string{fixtureStaticcheckGate, "STATICCHECK_EXTRA_FLAGS=-commandentry", fixtureTestFlagsOff}
	output, err = runConsumerMake(consumer, arguments...)
	if err != nil {
		t.Fatalf("formatting removed historical eligibility: %v\n%s", err, output)
	}

	if applied := readConsumerFile(t, consumer, appliedNoticeFile); applied != previous+commandNotice+"\n" {
		t.Fatalf("command notice did not apply:\n%s\n%s", applied, output)
	}

	baseline := readConsumerFile(t, consumer, baselineFileName)
	if !strings.Contains(baseline, "cmd/tool/main.go:") {
		t.Fatalf("notice omitted the formatted historical finding:\n%s", baseline)
	}

	writeConsumerFile(t, consumer, fixtureCommand, strings.ReplaceAll(formatted, "historical", "later"))
	output, err = runConsumerMake(consumer, arguments...)
	if err == nil || !strings.Contains(output, "commandentry:") {
		t.Fatalf("gate accepted changed implementation after notice application: %v\n%s", err, output)
	}
}
