package main_test

import (
	"os"
	"path/filepath"
	"testing"
)

func historyFailureConsumer(t *testing.T, scenario string) string {
	t.Helper()
	consumer := noticeGitRepository(t, noticeGitEnvironment(t), beforeNoticeDate)
	if err := os.Mkdir(filepath.Join(consumer, ".make"), 0o755); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join("testdata", "preparation.mk"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, "Makefile", string(contents))
	writeConsumerFile(t, consumer, "fixture.go", "package notice\n\nfunc Example() {}\n")
	if scenario == "preparation" {
		writeConsumerFile(t, consumer, "history-prepare-failure", "fail historical preparation\n")
	}

	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "-qm", "Record historical preparation")
	if scenario == "preparation" {
		if err := os.Remove(filepath.Join(consumer, "history-prepare-failure")); err != nil {
			t.Fatal(err)
		}
	}

	runConsumerGit(t, consumer, "2026-11-08T00:00:00Z", "add", ".")
	runConsumerGit(
		t,
		consumer,
		"2026-11-08T00:00:00Z",
		"commit",
		"--allow-empty",
		"-qm",
		"Record the current source",
	)

	return consumer
}
