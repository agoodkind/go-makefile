package staticcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"goodkind.io/go-makefile/staticcheck"
)

func TestTestSourceFile(t *testing.T) {
	t.Parallel()

	analysistest.Run(t, testHabitModule("testsourcefile"), staticcheck.TestSourceFileAnalyzer, "./...")
}
