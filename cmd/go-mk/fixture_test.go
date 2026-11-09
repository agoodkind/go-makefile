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
	consumer := duplicateReplayConsumer(t)
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
	original := readConsumerFile(t, consumer, "widget/widget.go")
	if scenario == "preparation" {
		writeConsumerFile(t, consumer, "history-prepare-failure", "fail historical preparation\n")
	} else {
		writeConsumerFile(
			t,
			consumer,
			"widget/widget.go",
			"package widget\n\nfunc Double(value int) int { return missingValue }\n",
		)
	}

	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(
		t,
		consumer,
		beforeNoticeDate,
		"commit",
		"-qm",
		"Record a real historical failure",
	)
	if scenario == "preparation" {
		if err := os.Remove(filepath.Join(consumer, "history-prepare-failure")); err != nil {
			t.Fatal(err)
		}
	} else {
		writeConsumerFile(t, consumer, "widget/widget.go", original)
	}

	runConsumerGit(t, consumer, "2026-11-08T00:00:00Z", "add", ".")
	runConsumerGit(
		t,
		consumer,
		"2026-11-08T00:00:00Z",
		"commit",
		"-qm",
		"Repair the current preparation and source",
	)

	return consumer
}
