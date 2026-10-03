package staticcheck

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

// analyzableTestFiles returns the hand-written _test.go files of the pass. The
// test-habit analyzers (testdouble, testpackage, testassert, testsourcefile)
// report only in these files.
func analyzableTestFiles(pass *analysis.Pass) []*ast.File {
	files := make([]*ast.File, 0, len(pass.Files))
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if !isTestFile(path) || isGeneratedFile(file, path) {
			continue
		}
		files = append(files, file)
	}
	return files
}
