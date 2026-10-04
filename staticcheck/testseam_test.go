package staticcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"goodkind.io/go-makefile/staticcheck"
)

func TestTestSeam(t *testing.T) {
	analysistest.Run(t, testHabitModule("testseam"), staticcheck.TestSeamAnalyzer, "./...")
}
