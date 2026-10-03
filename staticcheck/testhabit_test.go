package staticcheck_test

import (
	"path/filepath"

	"golang.org/x/tools/go/analysis/analysistest"
)

// testHabitModule returns the module root under testdata for one test-habit
// analyzer. Each root has its own go.mod, so analysistest loads the fixture
// packages in module mode together with their test variants.
func testHabitModule(analyzerName string) string {
	return filepath.Join(analysistest.TestData(), analyzerName)
}
