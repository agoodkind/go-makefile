package staticcheck

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"sort"

	"golang.org/x/tools/go/analysis"
)

// ArgumentLinesAnalyzer formats wrapped arguments and callback parameters.
var ArgumentLinesAnalyzer = &analysis.Analyzer{
	Name: "argumentlines",
	Doc:  "requires separate lines for wrapped arguments and callback declaration parameters",
	Run:  runArgumentLines,
}

func runArgumentLines(pass *analysis.Pass) (any, error) {
	sources, err := originalSources(pass)
	if err != nil {
		return nil, err
	}

	for _, source := range sources {
		var analysisErr error
		ast.Inspect(source.file, func(node ast.Node) bool {
			if analysisErr != nil {
				return false
			}

			position, kind, edits := layoutEdits(pass.Fset, node, source.contents)
			if len(edits) == 0 {
				return true
			}

			identity, err := occurrenceIdentity(source.file, node)
			if err != nil {
				analysisErr = err
				return false
			}

			name := declarationName(declarationFor(source.file, node))
			pass.Report(analysis.Diagnostic{
				Pos:     position,
				Message: fmt.Sprintf("argumentlines: put each %s on a separate line in %s [id=%s]", kind, name, identity),
				SuggestedFixes: []analysis.SuggestedFix{{
					Message:   "Put list elements on separate lines",
					TextEdits: edits,
				}},
			})

			return true
		})
		if analysisErr != nil {
			return nil, analysisErr
		}
	}

	return nil, nil
}

func layoutEdits(fileSet *token.FileSet, node ast.Node, contents []byte) (token.Pos, string, []analysis.TextEdit) {
	switch typed := node.(type) {
	case *ast.CallExpr:
		if len(typed.Args) < 2 {
			return token.NoPos, "", nil
		}

		elements := make([]listElement, 0, len(typed.Args))
		for _, argument := range typed.Args {
			elements = append(elements, listElement{start: argument.Pos(), end: argument.End()})
		}

		if typed.Ellipsis.IsValid() {
			elements[len(elements)-1].end = typed.Ellipsis + token.Pos(len("..."))
		}

		return typed.Lparen, "argument", listLineEdits(fileSet, typed.Lparen, typed.Rparen, elements, contents, false)
	case *ast.FuncType:
		if typed.Params == nil {
			return token.NoPos, "", nil
		}

		return typed.Params.Opening, "parameter", parameterLineEdits(fileSet, typed, contents)
	}

	return token.NoPos, "", nil
}

func argumentTrailingComma(contents []byte) bool {
	for len(contents) > 0 {
		contents = bytes.TrimSpace(contents)
		if bytes.HasPrefix(contents, []byte("/*")) {
			end := bytes.Index(contents[2:], []byte("*/"))
			if end < 0 {
				return false
			}

			contents = contents[end+4:]
			continue
		}

		return bytes.HasPrefix(contents, []byte(","))
	}

	return false
}

// FormatArguments formats argument lists and declarations with callbacks.
func FormatArguments(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("provide Go source files to format")
	}

	for _, path := range paths {
		if err := formatArgumentFile(path); err != nil {
			return fmt.Errorf("format arguments in %s: %w", path, err)
		}
	}

	return nil
}

func formatArgumentFile(path string) error {
	original, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read argument source: %w", err)
	}

	contents := original

	for {
		formatted, changed, err := formatArgumentSource(path, contents)
		if err != nil {
			return err
		}

		if !changed {
			break
		}

		if bytes.Equal(formatted, contents) {
			return fmt.Errorf("argument formatting did not stabilize")
		}

		contents = formatted
	}

	if bytes.Equal(original, contents) {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read argument source permissions: %w", err)
	}

	if err := os.WriteFile(path, contents, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write formatted arguments: %w", err)
	}

	return nil
}

func formatArgumentSource(path string, contents []byte) ([]byte, bool, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, contents, parser.ParseComments)
	if err != nil {
		return nil, false, fmt.Errorf("parse argument source: %w", err)
	}

	if isGeneratedFile(file, path) {
		return contents, false, nil
	}

	insertions := make(map[int]string)
	ast.Inspect(file, func(node ast.Node) bool {
		_, _, edits := layoutEdits(fileSet, node, contents)
		for _, edit := range edits {
			offset := fileSet.File(edit.Pos).Offset(edit.Pos)
			insertions[offset] += string(edit.NewText)
		}

		return true
	})
	if len(insertions) == 0 {
		return contents, false, nil
	}

	positions := make([]int, 0, len(insertions))
	for position := range insertions {
		positions = append(positions, position)
	}

	sort.Ints(positions)
	var updated bytes.Buffer
	previous := 0

	for _, position := range positions {
		updated.Write(contents[previous:position])
		updated.WriteString(insertions[position])
		previous = position
	}

	updated.Write(contents[previous:])
	formatted, err := format.Source(updated.Bytes())
	if err != nil {
		return nil, false, fmt.Errorf("format argument source: %w", err)
	}

	return formatted, true, nil
}
