package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDuplicateReplayPreservesTheOriginalCutoff(t *testing.T) {
	for _, scenario := range []string{
		"formatting",
		"later declaration changed",
		"later duplicate",
		"directive moved",
	} {
		t.Run(
			scenario,
			func(t *testing.T) {
				consumer := duplicateReplayConsumer(t)
				if scenario == "later declaration changed" {
					for _, name := range []string{"first", "second"} {
						path := "widget/" + name + ".go"
						original := readConsumerFile(t, consumer, path)
						writeConsumerFile(
							t,
							consumer,
							path,
							strings.Replace(original, "> 7", "> 9", 1),
						)
					}
				} else if scenario == "directive moved" {
					for _, name := range []string{"first", "second"} {
						path := "widget/" + name + ".go"
						original := readConsumerFile(t, consumer, path)
						changed := strings.Replace(original, "//go:noinline\n", "", 1)
						function := "func (store " + name + "Store) remove"
						changed = strings.Replace(changed, function, "//go:noinline\n"+function, 1)
						writeConsumerFile(t, consumer, path, changed)
					}
				} else if scenario == "later duplicate" {
					original := readConsumerFile(t, consumer, "widget/first.go")
					writeConsumerFile(
						t,
						consumer,
						"widget/later.go",
						strings.ReplaceAll(original, "firstStore", "laterStore"),
					)
				}

				runConsumerGit(t, consumer, "2026-11-08T00:00:00Z", "add", ".")
				runConsumerGit(
					t,
					consumer,
					"2026-11-08T00:00:00Z",
					"commit",
					"-qm",
					"Format and update consumer source",
				)
				writeConsumerFile(
					t,
					consumer,
					"dupl-notices.txt",
					"20\tINTRODUCED=2026-11-07T14:59:16-08:00 GATE=golangci LINTER=dupl\tReplay historical duplicates\n",
				)
				output, err := runConsumerMake(
					consumer,
					"go-mk-notice",
					"lint-golangci",
					"_GO_MK_NOTICES_FILE=dupl-notices.txt",
					"GOLANGCI_LINT_TARGETS=./widget",
				)
				baseline := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt")
				if scenario == "formatting" {
					if err != nil || strings.Count(baseline, "(dupl)") != 2 {
						t.Fatalf(
							"replay did not accept the formatted historical pair: %v\n%s\n%s",
							err,
							output,
							baseline,
						)
					}
				} else if err == nil || !strings.Contains(output, "(dupl)") {
					t.Fatalf(
						"replay accepted a post-cutoff implementation: %v\n%s\n%s",
						err,
						output,
						baseline,
					)
				} else if (scenario == "later declaration changed" || scenario == "directive moved") && strings.Contains(
					baseline,
					"(dupl)",
				) {
					t.Fatalf(
						"replay ignored a changed declaration after the unchanged first declaration:\n%s",
						baseline,
					)
				} else if strings.Contains(baseline, "widget/later.go:") {
					t.Fatalf("replay accepted a new duplicate occurrence:\n%s", baseline)
				}

				if applied := readConsumerFile(
					t,
					consumer,
					appliedNoticeFile,
				); applied != "4\n20\n" {
					t.Fatalf("replay changed the original acknowledgment: %q\n%s", applied, output)
				}
			},
		)
	}
}

func duplicateReplayConsumer(t *testing.T) string {
	t.Helper()
	consumer := newConsumerRepository(t, beforeNoticeDate)
	fixture, err := os.ReadFile(filepath.Join("testdata", "duplicate.go.txt"))
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"first", "second"} {
		writeConsumerFile(
			t,
			consumer,
			"widget/"+name+".go",
			strings.ReplaceAll(string(fixture), "firstStore", name+"Store"),
		)
	}

	writeConsumerFile(t, consumer, appliedNoticeFile, "4\n")
	writeConsumerFile(t, consumer, ".golangci-lint-baseline.txt", "# Existing baseline.\n")
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(
		t,
		consumer,
		beforeNoticeDate,
		"commit",
		"-qm",
		"Add historical duplicate declarations",
	)
	settings, err := os.ReadFile(filepath.Join("testdata", "format.yml"))
	if err != nil {
		t.Fatal(err)
	}

	configuration := strings.Replace(
		string(settings),
		"    - wsl_v5",
		"    - wsl_v5\n    - dupl",
		1,
	)
	writeConsumerFile(t, consumer, ".make/golangci.yml", configuration)
	writeConsumerFile(t, consumer, ".make/no-notices.txt", "")
	output, err := runConsumerMake(consumer, "fmt", "_GO_MK_NOTICES_FILE=.make/no-notices.txt")
	if err != nil {
		t.Fatalf("format duplicate consumer: %v\n%s", err, output)
	}

	return consumer
}
