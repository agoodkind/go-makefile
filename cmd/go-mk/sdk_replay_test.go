package main_test

import (
	"strings"
	"testing"
)

func TestCommandEntryReplayPreservesTheOriginalCutoff(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)
	const original = "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"original\") }\n"

	for _, path := range []string{"cmd/old/main.go", "cmd/changed/main.go"} {
		writeConsumerFile(t, consumerDir, path, original)
	}

	runConsumerGit(t, consumerDir, beforeNoticeDate, "add", ".")
	runConsumerGit(
		t,
		consumerDir,
		beforeNoticeDate,
		"commit",
		"--quiet",
		"--message",
		"Add historical commands",
	)
	const olderAcknowledgments = "1\n2\n3\n14\n15\n16\n18\n"
	writeConsumerFile(t, consumerDir, appliedNoticeFile, olderAcknowledgments)
	writeConsumerFile(
		t,
		consumerDir,
		"cmd/changed/main.go",
		strings.Replace(original, "original", "changed", 1),
	)
	writeConsumerFile(t, consumerDir, "cmd/later/main.go", original)
	const afterCommandEntryCutoff = "2026-11-08T06:00:00Z"
	runConsumerGit(t, consumerDir, afterCommandEntryCutoff, "add", ".")
	runConsumerGit(
		t,
		consumerDir,
		afterCommandEntryCutoff,
		"commit",
		"--quiet",
		"--message",
		"Add and change commands after the original cutoff",
	)
	writeConsumerFile(t, consumerDir, "cmd/uncommitted/main.go", original)
	arguments := []string{
		"staticcheck-extra",
		"STATICCHECK_EXTRA_FLAGS=-commandentry",
		"STATICCHECK_EXTRA_TEST_FLAGS=",
	}
	output, err := runConsumerMake(consumerDir, arguments...)
	if err == nil {
		t.Fatalf("gate accepted current command implementations:\n%s", output)
	}

	baseline := readConsumerFile(t, consumerDir, baselineFileName)
	if !strings.Contains(baseline, "cmd/old/main.go:") {
		t.Fatalf("replay omitted the historical implementation:\n%s\n%s", baseline, output)
	}

	for _, path := range []string{
		"cmd/changed/main.go:",
		"cmd/later/main.go:",
		"cmd/uncommitted/main.go:",
	} {
		if strings.Contains(baseline, path) || !strings.Contains(output, path) {
			t.Fatalf("replay did not reject %s:\n%s\n%s", path, baseline, output)
		}
	}

	if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied !=
		olderAcknowledgments+"19\n" {
		t.Fatalf("replay changed older acknowledgments: %q\n%s", applied, output)
	}

	output, err = runConsumerMake(consumerDir, arguments...)
	if err == nil {
		t.Fatalf("later gate accepted current implementations:\n%s", output)
	}

	if current := readConsumerFile(t, consumerDir, baselineFileName); current != baseline {
		t.Fatalf("acknowledged replay changed the baseline:\n%s", current)
	}
}
