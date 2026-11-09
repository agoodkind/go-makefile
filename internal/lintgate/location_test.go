package lintgate_test

import (
	"testing"

	"goodkind.io/go-makefile/internal/lintgate"
)

func TestNilErrorSourceLocationsPreserveBaseline(t *testing.T) {
	tests := []struct {
		before string
		after  string
		passed bool
	}{
		{
			before: "internal/github/comment.go:61:3: error is not nil (line 60) but it returns nil (nilerr)",
			after:  "internal/github/comment.go:64:3: error is not nil (line 63) but it returns nil (nilerr)",
			passed: true,
		},
		{
			before: "internal/review/verify.go:48:3: error is not nil (line 46) but it returns nil (nilerr)",
			after:  "internal/review/verify.go:66:3: error is not nil (line 64) but it returns nil (nilerr)",
			passed: true,
		},
		{
			before: "widget.go:1:1: error is not nil (line 60) but it returns nil (other)",
			after:  "widget.go:2:1: error is not nil (line 63) but it returns nil (other)",
		},
		{
			before: "widget.go:2:1: error is nil (line 1) but it returns error (nilerr)",
			after:  "widget.go:4:1: error is nil (line 3) but it returns error (nilerr)",
			passed: true,
		},
		{
			before: "widget.go:2:1: error is not nil (lines [60 65]) but it returns nil (nilerr)",
			after:  "widget.go:4:1: error is not nil (lines [63 70]) but it returns nil (nilerr)",
			passed: true,
		},
		{
			before: "widget.go:2:1: error is not nil (lines [60 65]) but it returns nil (nilerr)",
			after:  "widget.go:4:1: error is not nil (lines [63 66 70]) but it returns nil (nilerr)",
		},
		{
			before: "widget.go:1:1: error is not nil (line 60) but it returns nil (nilerr)",
			after:  "other.go:2:1: error is not nil (line 63) but it returns nil (nilerr)",
		},
	}
	for _, test := range tests {
		result := lintgate.Evaluate(
			"golangci-lint",
			[]string{test.after},
			[]string{test.before},
			nil,
			nil,
			"fix source",
		)
		if result.Passed != test.passed || (test.passed && result.GoneCount != 0) {
			t.Fatalf("source location changed the gate outcome: %+v", result)
		}
	}
}
