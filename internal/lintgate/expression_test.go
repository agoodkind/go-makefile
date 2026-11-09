package lintgate_test

import (
	"strconv"
	"testing"

	"goodkind.io/go-makefile/internal/lintgate"
)

func TestErrorExpressionFormattingPreservesBaseline(t *testing.T) {
	const prefix = "widget.go:1:1: do not define dynamic errors, use wrapped static errors instead: "
	const expression = `fmt.Errorf("original %d", value)`
	const wrapped = "fmt.Errorf(\n\t\"original %d\",\n\tvalue,\n)"
	const concatenated = `fmt.Errorf("ori" + "ginal %d", value)`
	baseline := []string{prefix + strconv.Quote(expression) + " (err113)"}
	formatted := prefix + strconv.Quote(wrapped) + " (err113)"
	joined := prefix + strconv.Quote(concatenated) + " (err113)"
	changed := prefix + strconv.Quote(`fmt.Errorf("changed %d", value)`) + " (err113)"
	spaceChanged := prefix + strconv.Quote(`fmt.Errorf("original  %d", value)`) + " (err113)"
	dynamic := prefix + strconv.Quote(`fmt.Errorf("ori" + suffix, value)`) + " (err113)"

	for _, current := range []string{formatted, joined, changed, spaceChanged, dynamic} {
		result := lintgate.Evaluate(
			"golangci-lint",
			[]string{current},
			baseline,
			nil,
			nil,
			"fix source",
		)
		if current == formatted || current == joined {
			if !result.Passed || result.GoneCount != 0 {
				t.Fatalf("formatting changed the gate outcome: %+v", result)
			}

			continue
		}

		if result.Passed || len(result.NewFindings) != 1 {
			t.Fatalf("changed literal did not fail the gate: %+v", result)
		}
	}
}
