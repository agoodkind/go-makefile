package staticcheck

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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

	checked := checkedSourceTypes(pass.Fset, pass.TypesInfo)

	for _, source := range sources {
		var analysisErr error
		ast.Inspect(source.file, func(node ast.Node) bool {
			if analysisErr != nil {
				return false
			}

			position, kind, edits, err := layoutEdits(pass.Fset, node, source.contents, checked)
			if err != nil {
				analysisErr = err
				return false
			}

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

func layoutEdits(fileSet *token.FileSet, node ast.Node, contents []byte, checked checkedTypes) (token.Pos, string, []analysis.TextEdit, error) {
	switch typed := node.(type) {
	case *ast.CallExpr:
		if len(typed.Args) < 2 {
			return token.NoPos, "", nil, nil
		}

		elements := make([]listElement, 0, len(typed.Args))
		for _, argument := range typed.Args {
			elements = append(elements, listElement{start: argument.Pos(), end: argument.End()})
		}

		if typed.Ellipsis.IsValid() {
			elements[len(elements)-1].end = typed.Ellipsis + token.Pos(len("..."))
		}

		return typed.Lparen, "argument", listLineEdits(fileSet, typed.Lparen, typed.Rparen, elements, contents, false), nil
	case *ast.FuncType:
		if typed.Params == nil {
			return token.NoPos, "", nil, nil
		}

		edits, err := parameterLineEdits(fileSet, typed, contents, checked)

		return typed.Params.Opening, "parameter", edits, err
	}

	return token.NoPos, "", nil, nil
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

type argumentFormatting struct {
	selected []string
	original map[string][]byte
	contents map[string][]byte
}

// FormatArguments formats argument lists and declarations with callbacks.
func FormatArguments(paths []string) error {
	formatted, err := prepareArgumentFormatting(paths)
	if err != nil {
		return err
	}

	selected, original, contents := formatted.selected, formatted.original, formatted.contents
	for _, path := range selected {
		if bytes.Equal(original[path], contents[path]) {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("read argument source permissions: %w", err)
		}

		if err := os.WriteFile(path, contents[path], info.Mode().Perm()); err != nil {
			return fmt.Errorf("write formatted arguments: %w", err)
		}
	}

	return nil
}

func prepareArgumentFormatting(paths []string) (argumentFormatting, error) {
	if len(paths) == 0 {
		return argumentFormatting{}, fmt.Errorf("provide Go source files to format")
	}

	original := make(map[string][]byte)
	contents := make(map[string][]byte)
	var selected []string

	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return argumentFormatting{}, fmt.Errorf("resolve argument source: %w", err)
		}

		absolute, err = filepath.EvalSymlinks(absolute)
		if err != nil {
			return argumentFormatting{}, fmt.Errorf("resolve argument source: %w", err)
		}

		if _, duplicate := original[absolute]; duplicate {
			continue
		}

		data, err := os.ReadFile(absolute)
		if err != nil {
			return argumentFormatting{}, fmt.Errorf("read argument source: %w", err)
		}

		original[absolute], contents[absolute] = data, data
		selected = append(selected, absolute)
	}

	groups, err := formattingPlatforms(selected)
	if err != nil {
		return argumentFormatting{}, err
	}

	for {
		checked, err := loadFormattingTypes(groups, contents)
		if err != nil {
			return argumentFormatting{}, err
		}

		changedAny := false

		for _, path := range selected {
			formatted, changed, err := formatArgumentSource(path, contents[path], checked[path])
			if err != nil {
				return argumentFormatting{}, fmt.Errorf("format arguments in %s: %w", path, err)
			}

			if !changed {
				continue
			}

			if bytes.Equal(formatted, contents[path]) {
				return argumentFormatting{}, fmt.Errorf("argument formatting did not stabilize in %s", path)
			}

			contents[path] = formatted
			changedAny = true
		}

		if !changedAny {
			break
		}
	}

	return argumentFormatting{selected: selected, original: original, contents: contents}, nil
}

func formatArgumentSource(path string, contents []byte, checked checkedTypes) ([]byte, bool, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, contents, parser.ParseComments)
	if err != nil {
		return nil, false, fmt.Errorf("parse argument source: %w", err)
	}

	insertions := make(map[int]string)
	var analysisErr error
	ast.Inspect(file, func(node ast.Node) bool {
		if analysisErr != nil {
			return false
		}

		_, _, edits, err := layoutEdits(fileSet, node, contents, checked)
		if err != nil {
			analysisErr = err
			return false
		}

		for _, edit := range edits {
			offset := fileSet.File(edit.Pos).Offset(edit.Pos)
			insertions[offset] += string(edit.NewText)
		}

		return true
	})
	if analysisErr != nil {
		return nil, false, analysisErr
	}

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
