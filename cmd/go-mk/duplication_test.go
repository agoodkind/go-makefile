package main_test

import (
	"os"
	"strings"
	"testing"
)

// TestDuplicateChecksUseAppliedNotices checks adoption of diagnostics
// without a column.
func TestDuplicateChecksUseAppliedNotices(test *testing.T) {
	engine := noticeGitBuildEngine(test, os.Environ())
	consumer := newConsumerRepository(test, beforeNoticeDate)
	lintPolicyConfiguration(test, consumer)
	writeConsumerFile(test, consumer, "notices.txt", lintPolicyNotice(test, "4"))

	const repetitions = 40

	body := "(value int) int {\n" + strings.Repeat("value += value * 2\n", repetitions) + "return value\n}\n"
	writeConsumerFile(test, consumer, "widget/first.go", "package widget\nfunc First"+body)
	writeConsumerFile(test, consumer, "widget/second.go", "package widget\nfunc Second"+body)
	runConsumerGit(test, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(test, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical duplication")

	output, err := lintPolicyMake(consumer, engine, "go-mk-notice")
	if err != nil {
		test.Fatalf("apply duplication notice: %v\n%s", err, output)
	}

	baseline := readConsumerFile(test, consumer, policyBaseline)
	if !strings.Contains(baseline, "widget/first.go:1:1:") || !strings.Contains(baseline, "(dupl)") {
		test.Fatalf("notice did not record the line-only diagnostic:\n%s\n%s", baseline, output)
	}

	options := []string{"lint-golangci", "GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=dupl"}

	output, err = lintPolicyMake(consumer, engine, options...)
	if err != nil {
		test.Fatalf("historical duplication failed the gate: %v\n%s", err, output)
	}

	writeConsumerFile(test, consumer, "widget/later.go", "package widget\nfunc Later"+body)
	output, err = lintPolicyMake(consumer, engine, options...)

	if err == nil || !strings.Contains(output, "widget/later.go:") {
		test.Fatalf("later duplication did not fail the gate: %v\n%s", err, output)
	}
}
