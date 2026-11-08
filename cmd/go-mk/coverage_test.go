package main_test

import (
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

const coverageProductionRow = "widget/preserved.go:7:2: historical production finding (errcheck)\n"

const coverageOldTest = `package widget_test

import (
	"os"
	"testing"
)

func TestHistorical(test *testing.T) {
	os.WriteFile("historical.txt", nil, 0o600)
}
`

const coverageCurrentProduction = `package widget

import "os"

// Current writes a production fixture.
func Current() {
	os.WriteFile("current.txt", nil, 0o600)
}
`

// TestTestCoverageUsesAppliedNotice checks historical test adoption and
// later failures.
func TestTestCoverageUsesAppliedNotice(test *testing.T) {
	engine := gomk.BuiltTestEngine(test)
	consumer := newConsumerRepository(test, beforeNoticeDate)
	lintPolicyConfiguration(test, consumer)
	writeConsumerFile(test, consumer, "notices.txt", lintPolicyNotice(test, "13"))
	writeConsumerFile(test, consumer, "widget/historical_test.go", coverageOldTest)
	writeConsumerFile(test, consumer, policyBaseline, coverageProductionRow)
	runConsumerGit(test, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(test, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical test finding")
	writeConsumerFile(test, consumer, "widget/current.go", coverageCurrentProduction)

	writeConsumerFile(test, consumer, appliedNoticeFile, "1\n13\n")
	output, err := coverageGate(consumer, engine)

	if err == nil ||
		!strings.Contains(output, "widget/historical_test.go:") ||
		!strings.Contains(output, "widget/current.go:") {
		test.Fatalf("test or production findings did not fail before adoption: %v\n%s", err, output)
	}

	writeConsumerFile(test, consumer, appliedNoticeFile, "1\n")
	baseline := coverageApplyNotice(test, consumer, engine)
	output, err = coverageGate(consumer, engine)

	if err == nil ||
		!strings.Contains(output, "widget/current.go:") ||
		strings.Contains(output, "widget/historical_test.go:") {
		test.Fatalf("adoption did not separate historical tests from production findings: %v\n%s", err, output)
	}

	writeConsumerFile(test, consumer, "widget/current.go", strings.Replace(coverageCurrentProduction,
		"os.WriteFile", "_ = os.WriteFile", 1))
	coverageRejectLaterTest(test, consumer, engine, baseline)
}

func coverageApplyNotice(t *testing.T, consumer, engine string) string {
	t.Helper()

	output, err := lintPolicyMake(consumer, engine, "go-mk-notice")
	if err != nil {
		t.Fatalf("apply test coverage notice: %v\n%s", err, output)
	}

	baseline := readConsumerFile(t, consumer, policyBaseline)
	if !strings.Contains(baseline, "widget/historical_test.go:") || strings.Contains(baseline, "widget/current.go:") {
		t.Fatalf("notice accepted the wrong source scope:\n%s\n%s", baseline, output)
	}

	if !strings.Contains(baseline, coverageProductionRow) {
		t.Fatalf("notice changed the prior production row:\n%s", baseline)
	}

	return baseline
}

func coverageRejectLaterTest(t *testing.T, consumer, engine, baseline string) {
	t.Helper()

	writeConsumerFile(t, consumer, "widget/later_test.go", `package widget_test
import ("os"; "testing")
func TestLater(test *testing.T){os.WriteFile("later.txt",nil,0o600)}
`)
	output, err := coverageGate(consumer, engine)

	if err == nil || !strings.Contains(output, "widget/later_test.go:") {
		t.Fatalf("later test finding did not fail: %v\n%s", err, output)
	}

	output, err = lintPolicyMake(consumer, engine, "lint-files", "LINT_FILES=widget/later_test.go",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=errcheck")
	if err == nil || !strings.Contains(output, "widget/later_test.go:") {
		t.Fatalf("scoped lint dropped the test finding: %v\n%s", err, output)
	}

	output, err = lintPolicyMake(consumer, engine, "lint-format", "GOLANGCI_FMT_FILES=widget/later_test.go")
	if err == nil || !strings.Contains(output, "widget/later_test.go") {
		t.Fatalf("formatter did not inspect the test file: %v\n%s", err, output)
	}

	output, err = lintPolicyMake(consumer, engine, "go-mk-notice")
	if err != nil {
		t.Fatalf("repeat applied notice: %v\n%s", err, output)
	}

	if current := readConsumerFile(t, consumer, policyBaseline); current != baseline {
		t.Fatalf("a later run extended the historical baseline:\n%s", current)
	}
}

func coverageGate(consumer, engine string) (string, error) {
	return lintPolicyMake(consumer, engine, "lint-golangci",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=errcheck")
}
