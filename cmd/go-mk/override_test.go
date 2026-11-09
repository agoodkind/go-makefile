package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryPreparationValidatesMakefileOverrides(t *testing.T) {
	for _, scenario := range []string{"relative", "absolute", "symlink", "nonregular"} {
		t.Run(
			scenario,
			func(t *testing.T) {
				consumer := duplicateReplayConsumer(t)
				override := "Makefile"
				switch scenario {
				case "relative":
				case "absolute":
					override = filepath.Join(consumer, "Makefile")
				case "symlink":
					override = "selected.mk"
					if err := os.Symlink(
						"Makefile",
						filepath.Join(consumer, override),
					); err != nil {
						t.Fatal(err)
					}
				case "nonregular":
					override = "invalid.mk"
					writeConsumerFile(t, consumer, "invalid.mk/marker.txt", "directory\n")
				}

				runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
				runConsumerGit(
					t,
					consumer,
					beforeNoticeDate,
					"commit",
					"-qm",
					"Record the selected Makefile",
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
					"GO_MK_NOTICE_MAKEFILE="+override,
				)
				baseline := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt")
				if scenario == "nonregular" {
					if err == nil ||
						!strings.Contains(
							output,
							"historical preparation requires a regular Makefile",
						) ||
						strings.Contains(baseline, "(dupl)") {
						t.Fatalf(
							"nonregular override did not fail before acceptance: %v\n%s\n%s",
							err,
							output,
							baseline,
						)
					}

					if applied := readConsumerFile(
						t,
						consumer,
						appliedNoticeFile,
					); applied != "4\n" {
						t.Fatalf("failed preparation acknowledged the replay: %q", applied)
					}

					return
				}

				if err != nil || strings.Count(baseline, "(dupl)") != 2 {
					t.Fatalf(
						"valid %s override failed: %v\n%s\n%s",
						scenario,
						err,
						output,
						baseline,
					)
				}
			},
		)
	}
}
