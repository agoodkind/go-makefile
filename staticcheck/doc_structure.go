package staticcheck

import (
	"go/ast"
	"go/doc/comment"

	"golang.org/x/tools/go/analysis"
)

// DocStructureAnalyzer checks the structure of exported function comments.
//
// Each existing comment must begin with an introduction and a separate
// explanation, followed by a list or preformatted block for an example or
// layout. This check verifies the block structure that Go's comment parser
// recognizes; it cannot establish the accuracy or usefulness of the content.
//
// A complete function comment can use this form:
//
//	// Double returns value multiplied by two.
//	//
//	// The multiplication uses Go's int arithmetic, including its overflow
//	// behavior, and returns zero when value is zero.
//	//
//	// For example:
//	//
//	//     Double(3) == 6
//	func Double(value int) int { return value * 2 }
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
				pass.Reportf(function.Doc.Pos(), "doc_structure: add an introduction, explanation, and example or layout")
			}
		}
	}
	return nil, nil
}
