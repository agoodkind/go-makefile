package main_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoticesDistinguishIdenticalInitializers checks duplicate
// declarations after notice application.
func TestNoticesDistinguishIdenticalInitializers(t *testing.T) {
	t.Parallel()
	consumer := newConsumerRepository(t, beforeNoticeDate)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/golangci.yml", readConsumerFile(t, repository, "golangci.yml"))
	previous := setPendingNotices(t, consumer, argumentsNotice, whitespaceNotice)
	const path = "widget/initializers.go"
	const preamble = "package widget\n\nfunc consume(first, second int) {}\n"
	const violating = `
func init() {
	if true {
		consume(0, 0)
	}
	consume(
		1, 2,
	)
}
`
	const formatted = `
func init() {
	if true {
		consume(0, 0)
	}

	consume(
		1,
		2,
	)
}
`
	writeConsumerFile(t, consumer, path, preamble+violating+formatted)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical initializers")
	flags := []string{
		fixtureArgumentFlags,
		fixtureTestFlagsOff,
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=wsl_v5",
	}
	output, err := runConsumerMake(consumer, append([]string{"go-mk-notice"}, flags...)...)
	expected := previous + argumentsNotice + "\n" + whitespaceNotice + "\n"
	if err != nil || readConsumerFile(t, consumer, appliedNoticeFile) != expected {
		t.Fatalf("historical notices failed: %v\n%s", err, output)
	}

	gates := []struct{ name, baseline, diagnostic, statement string }{
		{fixtureStaticcheckGate, baselineFileName, "argumentlines:", "\tconsume(\n"},
		{fixtureGolangciGate, ".golangci-lint-baseline.txt", "(after-block)", "\t}\n"},
	}

	for _, gate := range gates {
		baseline := readConsumerFile(t, consumer, gate.baseline)
		if !strings.Contains(baseline, path+":") || !strings.Contains(baseline, gate.diagnostic) {
			t.Fatalf("%s lacks the historical finding:\n%s", gate.baseline, baseline)
		}
	}

	rewrapped := strings.Replace(formatted, "consume(0, 0)", "consume(\n\t\t\t0,\n\t\t\t0,\n\t\t)", 1)
	for _, scenario := range []struct {
		name   string
		source string
		reject bool
	}{
		{"unrelated edit", preamble + "\nfunc other() {}\n" + violating + rewrapped, false},
		{"appended initializer", preamble + violating + formatted + violating, true},
		{"swapped formatting", preamble + formatted + violating, true},
	} {
		writeConsumerFile(t, consumer, path, scenario.source)
		runConsumerGit(t, consumer, afterNoticeDate, "add", path)
		runConsumerGit(t, consumer, afterNoticeDate, "commit", "--quiet", "--message", scenario.name)

		for _, gate := range gates {
			output, err := runConsumerMake(consumer, append([]string{gate.name}, flags...)...)
			if scenario.reject {
				before, _, _ := strings.Cut(violating, gate.statement)
				line := strings.Count(strings.TrimSuffix(scenario.source, violating)+before, "\n") + 1
				location := fmt.Sprintf("%s:%d:", path, line)
				if err == nil || !strings.Contains(output, location) || !strings.Contains(output, gate.diagnostic) {
					t.Errorf("%s did not reject the later initializer in %s: %v\n%s", gate.name, scenario.name, err, output)
				}
			} else if err != nil {
				t.Errorf("%s rejected the historical initializer after a line shift: %v\n%s", gate.name, err, output)
			}
		}
	}
}
