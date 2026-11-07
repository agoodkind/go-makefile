package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoticeRejectsFindingsCommittedAfterItsEffectiveDate(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		file       string
		commitDate string
		finding    string
	}{
		{"new file", "widget/later_test.go", afterNoticeDate, "TestDoubleRuns"},
		{"existing file", "widget/widget_test.go", afterNoticeDate, "TestLater"},
		{"effective date", "widget/later_test.go", "2026-10-05T07:00:00Z", "TestDoubleRuns"},
		{"uncommitted file", "widget/later_test.go", "", "TestDoubleRuns"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			consumerDir := newConsumerRepository(t, beforeNoticeDate)
			contents := assertionFreeTest
			if scenario.file == "widget/widget_test.go" {
				contents = readConsumerFile(t, consumerDir, scenario.file) + "\nfunc TestLater(t *testing.T) { Double(3) }\n"
			}
			writeConsumerFile(t, consumerDir, scenario.file, contents)
			if scenario.commitDate != "" {
				runConsumerGit(t, consumerDir, scenario.commitDate, "add", scenario.file)
				runConsumerGit(t, consumerDir, scenario.commitDate, "commit", "--quiet", "--message", "Add a later test")
			}

			output, err := runConsumerMake(consumerDir, "staticcheck-extra")
			if err == nil {
				t.Fatalf("gate passed on a finding added after the notice took effect:\n%s", output)
			}
			if !strings.Contains(output, scenario.finding) {
				t.Fatalf("gate output lacks the later finding:\n%s", output)
			}
			baseline := readConsumerFile(t, consumerDir, baselineFileName)
			if !strings.Contains(baseline, olderTestFinding) {
				t.Fatalf("baseline lacks the finding from before the notice:\n%s\nGate output:\n%s", baseline, output)
			}
			if strings.Contains(baseline, scenario.finding) {
				t.Fatalf("notice baselined a finding added after its effective date:\n%s", baseline)
			}
			if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != "1\n2\n" {
				t.Fatalf("applied notices = %q, want both notices after the historical capture succeeds", applied)
			}
		})
	}
}

func TestNoticeRejectsLaterSubtestsWithTheSameDiagnostic(t *testing.T) {
	const oldSource = "package widget_test\n\nimport (\"testing\"; \"example.com/consumer/widget\")\n\nfunc TestCases(t *testing.T) {\n\tt.Run(\"old\", func(t *testing.T) { widget.Double(2) })\n}\n"
	for _, scenario := range []struct {
		name   string
		source string
	}{
		{"additional subtest", strings.Replace(oldSource, "\n}\n", "\n\tt.Run(\"later\", func(t *testing.T) { widget.Double(2) })\n}\n", 1)},
		{"renamed subtest", strings.Replace(oldSource, "\"old\"", "\"later\"", 1)},
		{"different test", strings.Replace(oldSource, "TestCases", "TestLater", 1)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			consumerDir := newConsumerRepository(t, beforeNoticeDate)
			writeConsumerFile(t, consumerDir, "widget/subtests_test.go", oldSource)
			runConsumerGit(t, consumerDir, beforeNoticeDate, "add", "widget/subtests_test.go")
			runConsumerGit(t, consumerDir, beforeNoticeDate, "commit", "--quiet", "--message", "Add an earlier subtest")
			writeConsumerFile(t, consumerDir, "widget/subtests_test.go", scenario.source)
			runConsumerGit(t, consumerDir, afterNoticeDate, "add", "widget/subtests_test.go")
			runConsumerGit(t, consumerDir, afterNoticeDate, "commit", "--quiet", "--message", "Add a later subtest")

			output, err := runConsumerMake(consumerDir, "staticcheck-extra")
			if err == nil {
				t.Fatalf("gate passed with a later occurrence of a historical diagnostic:\n%s", output)
			}
			const diagnostic = "This subtest has no statement able to fail"
			if !strings.Contains(output, diagnostic) {
				t.Fatalf("gate output lacks the repeated diagnostic:\n%s", output)
			}
			if baseline := readConsumerFile(t, consumerDir, baselineFileName); strings.Contains(baseline, diagnostic) {
				t.Fatalf("notice accepted a group containing a later finding:\n%s", baseline)
			}
		})
	}
}

func TestNoticePreparesHistoricalGeneratedSource(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)
	makefile := "GO_MK_GENERATE := generate-widget\n" + readConsumerFile(t, consumerDir, "Makefile") + "\ngenerate-widget:\n\tcp generated.go.txt widget/generated.go\n"
	writeConsumerFile(t, consumerDir, "Makefile", makefile)
	writeConsumerFile(t, consumerDir, ".gitignore", ".make/\nwidget/generated.go\n")
	writeConsumerFile(t, consumerDir, "generated.go.txt", "package widget\n\nconst generatedMultiplier = 2\n")
	writeConsumerFile(t, consumerDir, "widget/widget.go", "package widget\n\nfunc Double(value int) int { return value * generatedMultiplier }\n")
	runConsumerGit(t, consumerDir, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumerDir, beforeNoticeDate, "commit", "--quiet", "--message", "Add generated source")

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("notice failed to analyze the prepared historical source:\n%v\n%s", err, output)
	}
	if baseline := readConsumerFile(t, consumerDir, baselineFileName); !strings.Contains(baseline, olderTestFinding) {
		t.Fatalf("baseline lacks the historical finding:\n%s", baseline)
	}
	if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != "1\n2\n" {
		t.Fatalf("applied notices = %q, want both notices", applied)
	}
}

func TestNoticeDoesNotApplyWhenHistoricalSourceDoesNotBuild(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)
	original := readConsumerFile(t, consumerDir, "widget/widget.go")
	writeConsumerFile(t, consumerDir, "widget/widget.go", "package widget\n\nfunc Double(value int) int { return missingValue }\n")
	runConsumerGit(t, consumerDir, beforeNoticeDate, "add", "widget/widget.go")
	runConsumerGit(t, consumerDir, beforeNoticeDate, "commit", "--quiet", "--message", "Add source with a build error")
	writeConsumerFile(t, consumerDir, "widget/widget.go", original)
	runConsumerGit(t, consumerDir, afterNoticeDate, "add", "widget/widget.go")
	runConsumerGit(t, consumerDir, afterNoticeDate, "commit", "--quiet", "--message", "Fix the build error")

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err == nil || !strings.Contains(output, "missingValue") {
		t.Fatalf("gate did not report the historical build error:\n%v\n%s", err, output)
	}
	if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != "1\n" {
		t.Fatalf("applied notices = %q after a failed historical capture", applied)
	}
	if _, err := os.Stat(filepath.Join(consumerDir, baselineFileName)); !os.IsNotExist(err) {
		t.Fatalf("baseline stat error = %v, want the failed notice to leave the baseline absent", err)
	}
}

func TestNoticePreservesExplicitMakefile(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)
	makefile := readConsumerFile(t, consumerDir, "Makefile")
	writeConsumerFile(t, consumerDir, "pipeline.mk", makefile)
	if err := os.Remove(filepath.Join(consumerDir, "Makefile")); err != nil {
		t.Fatal(err)
	}
	runConsumerGit(t, consumerDir, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumerDir, beforeNoticeDate, "commit", "--quiet", "--message", "Use an explicit Makefile")
	output, err := runConsumerMake(consumerDir, "-f", "pipeline.mk", "GO_MK_NOTICE_MAKEFILE=pipeline.mk", "staticcheck-extra")
	if err != nil {
		t.Fatalf("notice failed with an explicit Makefile: %v\n%s", err, output)
	}
	if baseline := readConsumerFile(t, consumerDir, baselineFileName); !strings.Contains(baseline, olderTestFinding) {
		t.Fatalf("notice did not baseline the historical finding:\n%s\n%s", baseline, output)
	}
}

func TestDocStructureNoticeRejectsChangedComment(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)
	writeConsumerFile(t, consumerDir, "widget/widget.go", "package widget\n\n// Double multiplies by two.\nfunc Double(value int) int { return value * 2 }\n")
	writeConsumerFile(t, consumerDir, "widget/old.go", "package widget\n\n// Old returns one.\nfunc Old() int { return 1 }\n")
	runConsumerGit(t, consumerDir, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumerDir, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical comments")
	writeConsumerFile(t, consumerDir, "widget/widget.go", "package widget\n\n// Double returns twice the input.\nfunc Double(value int) int { return value * 2 }\n")
	runConsumerGit(t, consumerDir, afterNoticeDate, "add", ".")
	runConsumerGit(t, consumerDir, afterNoticeDate, "commit", "--quiet", "--message", "Change a comment after the notice")
	writeConsumerFile(t, consumerDir, "doc-notice.txt", "3\tINTRODUCED=2026-11-07T13:41:31-08:00 GATE=staticcheck-extra PATTERN=doc_structure:\tRequire comment structure\n")
	output, err := runConsumerMake(consumerDir, "staticcheck-extra", "STATICCHECK_EXTRA_FLAGS=-doc_structure", "STATICCHECK_EXTRA_TEST_FLAGS=", "_GO_MK_NOTICES_FILE=doc-notice.txt")
	if err == nil || !strings.Contains(output, "doc_structure") {
		t.Fatalf("gate did not reject the changed comment: %v\n%s", err, output)
	}
	baseline := readConsumerFile(t, consumerDir, baselineFileName)
	if !strings.Contains(baseline, "old.go") || strings.Contains(baseline, "widget.go") {
		t.Fatalf("notice must baseline only the unchanged comment:\n%s\n%s", baseline, output)
	}
}
