package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWhitespaceNoticeRejectsLaterOccurrences checks new spacing
// violations in an existing file.
func TestWhitespaceNoticeRejectsLaterOccurrences(t *testing.T) {
	t.Parallel()
	consumer := newConsumerRepository(t, beforeNoticeDate)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	configuration, err := os.ReadFile(filepath.Join(repository, "golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/golangci.yml", string(configuration))
	previous := setPendingNotices(t, consumer, whitespaceNotice)
	historical := `package widget

func Spacing(value int) int {
	if value < 0 {
		value++
	}
	value++
	if value < 0 {
		value++
	}

	value++

	return value
}
`
	writeConsumerFile(t, consumer, "widget/spacing.go", historical)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical spacing")
	flags := "GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=wsl_v5"
	output, err := runConsumerMake(consumer, "go-mk-notice", flags)
	if err != nil {
		t.Fatalf("whitespace notice failed: %v\n%s", err, output)
	}

	if applied := readConsumerFile(t, consumer, appliedNoticeFile); applied != previous+whitespaceNotice+"\n" {
		t.Fatalf("whitespace notice did not apply:\n%s\n%s", applied, output)
	}

	baseline := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt")
	if !strings.Contains(baseline, "[source:") || !strings.Contains(baseline, "(wsl_v5)") {
		t.Fatalf("whitespace notice did not identify the historical finding:\n%s", baseline)
	}

	if output, err := runConsumerMake(consumer, fixtureGolangciGate, flags); err != nil {
		t.Fatalf("historical whitespace finding failed its gate: %v\n%s", err, output)
	}

	shifted := "// Package widget exercises spacing checks.\n" + historical
	writeConsumerFile(t, consumer, "widget/spacing.go", shifted)
	if output, err := runConsumerMake(consumer, fixtureGolangciGate, flags); err != nil {
		t.Fatalf("source line shifts changed the historical identity: %v\n%s", err, output)
	}

	for _, scenario := range []struct {
		name   string
		source string
	}{
		{
			name: "added",
			source: strings.Replace(historical, "\n\treturn value", `
	if value < 0 {
		value++
	}
	value++

	return value`, 1),
		},
		{
			name: "swapped",
			source: strings.Replace(
				strings.Replace(
					historical,
					"\t}\n\tvalue++",
					"\t}\n\n\tvalue++",
					1,
				),
				"\t}\n\n\tvalue++\n\n\treturn",
				"\t}\n\tvalue++\n\n\treturn",
				1,
			),
		},
	} {
		writeConsumerFile(t, consumer, "widget/spacing.go", scenario.source)
		output, err := runConsumerMake(consumer, fixtureGolangciGate, flags)
		if err == nil || !strings.Contains(output, "widget/spacing.go:") || !strings.Contains(output, "(after-block)") {
			t.Fatalf("whitespace gate accepted %s: %v\n%s", scenario.name, err, output)
		}

		if current := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt"); current != baseline {
			t.Fatalf("whitespace gate changed the applied baseline in %s:\n%s", scenario.name, current)
		}
	}
}
