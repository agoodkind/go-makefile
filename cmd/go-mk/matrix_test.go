package main_test

import (
	"strings"
	"testing"
)

func TestNoticeRecordsProductionFindingsForAnotherPlatform(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	writeConsumerFile(t, consumer, "cmd/tool/main.go", "package main\nfunc main() {}\n")
	writeConsumerFile(
		t,
		consumer,
		"cmd/tool/work_linux.go",
		"package main\nfunc work() { for {} }\n",
	)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "-qm", "Add historical Linux command")
	output, err := runConsumerMake(
		consumer,
		"staticcheck-extra",
		"GO_MK_PLATFORMS=freebsd/amd64 linux/amd64",
		"STATICCHECK_EXTRA_FLAGS=-commandentry",
	)
	if err != nil {
		t.Fatalf("platform gates failed after historical capture: %v\n%s", err, output)
	}

	baseline := readConsumerFile(t, consumer, baselineFileName)
	if !strings.Contains(baseline, "cmd/tool/work_linux.go:2:1: commandentry:") {
		t.Fatalf("historical Linux declaration is missing from the baseline:\n%s", baseline)
	}
}
