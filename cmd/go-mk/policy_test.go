package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

const policyBaseline = ".golangci-lint-baseline.txt"

// TestCompatibleChecksUseAppliedNotices checks historical adoption and
// later error rejection.
func TestCompatibleChecksUseAppliedNotices(test *testing.T) {
	engine := gomk.BuiltTestEngine(test)
	consumer := newConsumerRepository(test, beforeNoticeDate)
	lintPolicyConfiguration(test, consumer)
	writeConsumerFile(test, consumer, "notices.txt", lintPolicyNotice(test, "4"))
	writeConsumerFile(test, consumer, "widget/old.go", `package widget

import "os"

// Old writes a historical fixture.
func Old() {
	os.WriteFile("historical.txt", nil, 0o600)
}

// Names extends the scope of short variables.
func Names() int {
	fn, ok := 1, true
	value := 0
	value++
	value += 1
	value *= 1
	value -= 1
	value /= 1
	value %= 1
	if ok {
		return fn + value
	}
	return value
}
`)
	runConsumerGit(test, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(test, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical finding")

	output, err := lintPolicyMake(consumer, engine, "go-mk-notice")
	if err != nil {
		test.Fatalf("apply notice: %v\n%s", err, output)
	}

	baseline := readConsumerFile(test, consumer, policyBaseline)
	if !strings.Contains(baseline, "widget/old.go:") || !strings.Contains(baseline, "(errcheck)") {
		test.Fatalf("notice did not record the historical ignored error:\n%s\n%s", baseline, output)
	}

	for _, name := range []string{"fn", "ok"} {
		if !strings.Contains(baseline, "variable name '"+name+"' is too short") {
			test.Fatalf("notice dropped a diagnostic at a shared location for %s:\n%s", name, baseline)
		}
	}

	options := []string{"lint-golangci", "GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=errcheck"}

	output, err = lintPolicyMake(consumer, engine, options...)
	if err != nil {
		test.Fatalf("historical ignored error failed the gate: %v\n%s", err, output)
	}

	writeConsumerFile(test, consumer, "widget/later.go", `package widget

import "os"

// Later writes a later fixture.
func Later() {
	os.WriteFile("later.txt", nil, 0o600)
}
`)
	output, err = lintPolicyMake(consumer, engine, options...)

	if err == nil || !strings.Contains(output, "widget/later.go:") {
		test.Fatalf("later ignored error did not fail the gate: %v\n%s", err, output)
	}

	current := readConsumerFile(test, consumer, policyBaseline)
	if current != baseline {
		test.Fatalf("a later gate changed the applied baseline:\n%s", current)
	}
}

// TestCompatibleChecksPreserveExplicitErrorDiscard checks deliberate
// blank assignments.
func TestCompatibleChecksPreserveExplicitErrorDiscard(test *testing.T) {
	engine := gomk.BuiltTestEngine(test)
	consumer := newConsumerRepository(test, afterNoticeDate)
	lintPolicyConfiguration(test, consumer)
	writeConsumerFile(test, consumer, "widget/discard.go", `package widget

import "os"

// Discard explicitly ignores a write error.
func Discard() {
	_ = os.WriteFile("discarded.txt", nil, 0o600)
}
`)

	output, err := lintPolicyMake(
		consumer,
		engine,
		"lint-golangci",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=errcheck",
	)
	if err != nil {
		test.Fatalf("explicit error discard failed: %v\n%s", err, output)
	}
}

func lintPolicyConfiguration(t *testing.T, consumer string) {
	t.Helper()

	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join(repository, "golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/golangci.yml", string(contents))
}

func lintPolicyNotice(t *testing.T, identifier string) string {
	t.Helper()

	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join(repository, "notices.txt"))
	if err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(string(contents), "\n") {
		if strings.HasPrefix(line, identifier+"\t") {
			return line + "\n"
		}
	}

	t.Fatalf("notice %s does not exist", identifier)

	return ""
}

func lintPolicyMake(consumer, engine string, arguments ...string) (string, error) {
	const environmentCount = 2

	options := make([]string, 0, environmentCount+len(arguments))
	options = append(
		options,
		"GO_MK_BIN="+engine,
		"_GO_MK_NOTICES_FILE="+filepath.Join(consumer, "notices.txt"),
	)
	options = append(options, arguments...)

	return runConsumerMake(consumer, options...)
}
