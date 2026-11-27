package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

func historyFailureConsumer(t *testing.T, scenario string) string {
	t.Helper()
	consumer := newConsumerRepository(t, beforeNoticeDate)
	contents, err := os.ReadFile(filepath.Join("testdata", "preparation.mk"))
	if err != nil {
		t.Fatal(err)
	}

	preparation, err := template.New("preparation").Parse(string(contents))
	if err != nil {
		t.Fatal(err)
	}

	var rendered strings.Builder
	if err := preparation.Execute(
		&rendered,
		struct{ Engine string }{Engine: gomk.BuiltTestEngine(t)},
	); err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(
		t,
		consumer,
		"Makefile",
		rendered.String()+"\n"+readConsumerFile(t, consumer, "Makefile"),
	)
	if scenario == "preparation" {
		writeConsumerFile(t, consumer, "history-prepare-failure", "fail historical preparation\n")
	}

	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(
		t,
		consumer,
		beforeNoticeDate,
		"commit",
		"-qm",
		"Record historical preparation",
	)
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
