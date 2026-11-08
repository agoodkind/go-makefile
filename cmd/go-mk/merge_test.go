package main_test

import (
	"strings"
	"testing"
)

func TestNoticeUsesHistoricalSourceFromMergedBranch(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	runConsumerGit(t, consumer, "2026-02-01T00:00:00Z", "checkout", "--quiet", "-b", "historical")
	writeConsumerFile(t, consumer, "widget/merged_test.go", assertionFreeTest)
	runConsumerGit(t, consumer, "2026-02-01T00:00:00Z", "add", ".")
	runConsumerGit(t, consumer, "2026-02-01T00:00:00Z", "commit", "--quiet", "--message", "Add historical test")
	runConsumerGit(t, consumer, afterNoticeDate, "checkout", "--quiet", "main")
	writeConsumerFile(t, consumer, "README.md", "Consumer test fixture.\n")
	runConsumerGit(t, consumer, afterNoticeDate, "add", ".")
	runConsumerGit(t, consumer, afterNoticeDate, "commit", "--quiet", "--message", "Update consumer")
	runConsumerGit(t, consumer, afterNoticeDate, "merge", "--quiet", "--no-ff", "--message", "Merge historical test", "historical")

	output, err := runConsumerMake(consumer, "staticcheck-extra")
	if err != nil {
		t.Fatalf("gate rejected a finding committed before the notice on a merged branch: %v\n%s", err, output)
	}

	baseline := readConsumerFile(t, consumer, baselineFileName)
	if !strings.Contains(baseline, "widget/merged_test.go:") {
		t.Fatalf("baseline omitted the historical merged finding:\n%s", baseline)
	}

	writeConsumerFile(t, consumer, "widget/later_test.go", strings.ReplaceAll(assertionFreeTest, "TestDoubleRuns", "TestLaterDoubleRuns"))
	output, err = runConsumerMake(consumer, "staticcheck-extra")
	if err == nil || !strings.Contains(output, "widget/later_test.go:") {
		t.Fatalf("gate accepted a finding added after notice application: %v\n%s", err, output)
	}

	if current := readConsumerFile(t, consumer, baselineFileName); current != baseline {
		t.Fatalf("later gate changed the historical baseline:\n%s", current)
	}
}

func TestNoticePreservesHistoricalIdentityAfterFormatting(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	writeConsumerFile(t, consumer, "notices.txt", "3\tINTRODUCED=2026-11-07T13:22:28-08:00 GATE=staticcheck-extra PATTERN=commandentry:\tKeep command entry points thin.\n")
	writeConsumerFile(t, consumer, appliedNoticeFile, "1\n2\n3\n")
	writeConsumerFile(t, consumer, ".make/golangci.yml", "version: \"2\"\nformatters:\n  enable:\n    - gofumpt\n")
	original := "package main\nfunc main(){println( \"historical\" )}\nfunc helper(first, second int, callback func()) { println(first, second); callback() }\n"
	writeConsumerFile(t, consumer, "cmd/tool/main.go", original)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical command")

	output, err := runConsumerMake(consumer, "fmt", "GOLANGCI_FMT_FILES=cmd/tool/main.go")
	if err != nil {
		t.Fatalf("format historical source: %v\n%s", err, output)
	}

	formatted := readConsumerFile(t, consumer, "cmd/tool/main.go")
	if formatted == original {
		t.Fatal("formatter did not change the historical source layout")
	}

	writeConsumerFile(t, consumer, appliedNoticeFile, "1\n2\n")
	arguments := []string{"staticcheck-extra", "STATICCHECK_EXTRA_FLAGS=-commandentry", "STATICCHECK_EXTRA_TEST_FLAGS="}
	output, err = runConsumerMake(consumer, arguments...)
	if err != nil {
		t.Fatalf("formatting removed historical eligibility: %v\n%s", err, output)
	}

	baseline := readConsumerFile(t, consumer, baselineFileName)
	if !strings.Contains(baseline, "cmd/tool/main.go:") {
		t.Fatalf("notice omitted the formatted historical finding:\n%s", baseline)
	}

	writeConsumerFile(t, consumer, "cmd/tool/main.go", strings.ReplaceAll(formatted, "historical", "later"))
	output, err = runConsumerMake(consumer, arguments...)
	if err == nil || !strings.Contains(output, "commandentry:") {
		t.Fatalf("gate accepted changed implementation after notice application: %v\n%s", err, output)
	}
}
