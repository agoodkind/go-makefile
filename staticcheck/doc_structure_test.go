package staticcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"goodkind.io/go-makefile/staticcheck"
)

func TestDocStructure(t *testing.T) {
	t.Parallel()
	analysistest.Run(t, analysistest.TestData(), staticcheck.DocStructureAnalyzer, "docstructure")
}
