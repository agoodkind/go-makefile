package staticcheck

import (
	"go/ast"
	"go/doc/comment"

	"golang.org/x/tools/go/analysis"
)

// DocStructureAnalyzer checks the structure of exported function comments.
var DocStructureAnalyzer = &analysis.Analyzer{
	Name: "doc_structure",
	Doc:  "require an introduction, explanation, and example or layout in exported function comments",
	Run:  runDocStructure,
}

func runDocStructure(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file, path) {
			continue
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || !function.Name.IsExported() || function.Doc == nil {
				continue
			}
			var parser comment.Parser
			blocks := parser.Parse(function.Doc.Text()).Content
			introduction, explanation := false, false
			if len(blocks) >= 2 {
				_, introduction = blocks[0].(*comment.Paragraph)
				_, explanation = blocks[1].(*comment.Paragraph)
			}
			illustration := false
			for index, block := range blocks {
				if index < 2 {
					continue
				}
				switch block.(type) {
				case *comment.Code, *comment.List:
					illustration = true
				}
			}
			if !introduction || !explanation || !illustration {
				pass.Reportf(function.Pos(), "doc_structure: add an introduction, explanation, and example or layout")
			}
		}
	}
	return nil, nil
}
