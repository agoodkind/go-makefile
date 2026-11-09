package main_test

import (
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

const compositionSource = `package widget

import "strconv"

func item(text string) string {
	return ` + "`0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789`" + ` +
		` + "`\"content\":[{\"type\":\"output_text\",\"text\":`" + ` + strconv.Quote(text) + ` + "`}],`" + `
}
`

const compositionConfiguration = `version: "2"
run:
  relative-path-mode: wd
linters:
  default: none
  enable: [forbidigo]
formatters:
  enable: [gofumpt, goimports, golines]
  settings:
    golines:
      max-len: 100
      reformat-tags: false
      chain-split-dots: false
`

func compositionConsumer(t *testing.T, source string) (string, []string) {
	t.Helper()
	consumer := newConsumerRepository(t, beforeNoticeDate)
	writeConsumerFile(t, consumer, ".make/composition.yml", compositionConfiguration)
	writeConsumerFile(t, consumer, "widget/composition.go", source)

	return consumer, []string{
		"GO_MK_BIN=" + gomk.BuiltTestEngine(t),
		"GOLANGCI_LINT_FLAGS=-c .make/composition.yml",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/composition.yml --allow-parallel-runners --output.text.print-issued-lines=false",
		"GOLANGCI_FMT_FILES=widget/composition.go",
		"GOLANGCI_LINT_TARGETS=./widget",
	}
}

func TestMakeFormatCompositionAcceptsCanonicalSource(t *testing.T) {
	consumer, arguments := compositionConsumer(t, compositionSource)

	for _, target := range []string{"lint-format", "lint-golangci"} {
		output, err := runConsumerMake(consumer, append([]string{target}, arguments...)...)
		if err != nil {
			t.Fatalf("%s rejected the complete formatter result: %v\n%s", target, err, output)
		}

		if actual := readConsumerFile(
			t,
			consumer,
			"widget/composition.go",
		); actual != compositionSource {
			t.Fatalf("%s modified the checked source:\n%s", target, actual)
		}
	}
}

func TestMakeFormatCompositionRejectsUnformattedSource(t *testing.T) {
	const source = "package widget\nfunc message()string{return \"hello\"}"
	consumer, arguments := compositionConsumer(t, source)
	output, err := runConsumerMake(consumer, append([]string{"lint-format"}, arguments...)...)
	if err == nil || !strings.Contains(output, "composition.go") {
		t.Fatalf("format gate accepted unformatted source: %v\n%s", err, output)
	}

	if actual := readConsumerFile(t, consumer, "widget/composition.go"); actual != source {
		t.Fatalf("format gate modified unformatted source:\n%s", actual)
	}

	output, err = runConsumerMake(consumer, append([]string{"fmt"}, arguments...)...)
	if err != nil {
		t.Fatalf("format source: %v\n%s", err, output)
	}

	output, err = runConsumerMake(consumer, append([]string{"lint-format"}, arguments...)...)
	if err != nil {
		t.Fatalf("format gate rejected formatted source: %v\n%s", err, output)
	}
}

func TestMakeFormatCompositionRetainsUnrelatedFinding(t *testing.T) {
	source := compositionSource + "\nfunc Print() { println(\"rejected\") }\n"
	consumer, arguments := compositionConsumer(t, source)
	output, err := runConsumerMake(consumer, append([]string{"fmt"}, arguments...)...)
	if err != nil {
		t.Fatalf("format source: %v\n%s", err, output)
	}

	formatted := readConsumerFile(t, consumer, "widget/composition.go")
	output, err = runConsumerMake(consumer, append([]string{"lint-golangci"}, arguments...)...)
	if err == nil || !strings.Contains(output, "(forbidigo)") ||
		strings.Contains(output, "(golines)") {
		t.Fatalf("unrelated finding did not remain rejected: %v\n%s", err, output)
	}

	if actual := readConsumerFile(t, consumer, "widget/composition.go"); actual != formatted {
		t.Fatalf("lint gate modified source:\n%s", actual)
	}
}

func TestMakeFormatCompositionPreservesFormatterFailure(t *testing.T) {
	const source = "package widget\nfunc broken(\n"
	consumer, arguments := compositionConsumer(t, source)
	output, err := runConsumerMake(consumer, append([]string{"lint-format"}, arguments...)...)
	if err == nil {
		t.Fatalf("format gate accepted invalid source:\n%s", output)
	}

	if actual := readConsumerFile(t, consumer, "widget/composition.go"); actual != source {
		t.Fatalf("failed formatter modified source:\n%s", actual)
	}
}
