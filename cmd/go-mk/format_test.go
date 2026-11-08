package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakeFmtAppliesSourceRules formats a real consumer and preserves short calls.
func TestMakeFmtAppliesSourceRules(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	settings, err := os.ReadFile(filepath.Join("testdata", "format.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/format.yml", string(settings))
	writeConsumerFile(t, consumer, "widget/widget.go", `package widget

func sum(first, second int) int { return first + second }
func Double(value int) int { return sum(
value, value,
) }
func Apply(value int, callback func(int) int) int {
if value < 0 { return 0 }
return callback(value)
}
`)
	arguments := []string{"fmt", "GOLANGCI_LINT_FLAGS=-c .make/format.yml", "GOLANGCI_FMT_FILES=widget/widget.go"}
	output, err := runConsumerMake(consumer, arguments...)
	if err != nil {
		t.Fatalf("format consumer: %v\n%s", err, output)
	}

	formatted := readConsumerFile(t, consumer, "widget/widget.go")
	if !strings.Contains(formatted, "\n\t\tvalue,\n\t\tvalue,\n") || !strings.Contains(formatted, "func Apply(\n") {
		t.Fatalf("argument layout was not applied:\n%s", formatted)
	}

	if !strings.Contains(formatted, "\n\t}\n\n\treturn callback(value)") {
		t.Fatalf("whitespace fixes were not applied:\n%s", formatted)
	}

	output, err = runConsumerMake(consumer, arguments...)
	if err != nil || readConsumerFile(t, consumer, "widget/widget.go") != formatted {
		t.Fatalf("second formatting run was not stable: %v\n%s", err, output)
	}
}
