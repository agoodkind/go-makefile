package staticcheck

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

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
