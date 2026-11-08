package staticcheck

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"

	"golang.org/x/tools/go/analysis"
)

const minimumWrappedArguments = 2

var errArgumentSource = errors.New("provide a Go source file")

var errArgumentType = errors.New("parameter type information is required")

// ArgumentLinesAnalyzer checks wrapped arguments and callback parameters.
var ArgumentLinesAnalyzer = newArgumentLinesAnalyzer()

func newArgumentLinesAnalyzer() *analysis.Analyzer {
	selected := make(map[string]bool)
	analyzer := &analysis.Analyzer{
		Name: "argumentlines",
		Doc:  "separate wrapped arguments and callback parameters",
		Run:  nil,
	}
	analyzer.Flags.Func("file", "limit fixes to this source file; repeat for each selected file", func(path string) error {
		if path == "" {
			return errArgumentSource
		}

		resolved, err := argumentSourcePath(path)
		if err == nil {
			selected[resolved] = true
		}

		return err
	})
	analyzer.Run = func(pass *analysis.Pass) (any, error) {
		return runArgumentLines(pass, selected)
	}

	return analyzer
}

func argumentSourcePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve argument source: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve argument source: %w", err)
	}

	return resolved, nil
}

func runArgumentLines(pass *analysis.Pass, selected map[string]bool) (any, error) {
	for _, file := range pass.Files {
		path := pass.Fset.File(file.Pos()).Name()
		if len(selected) > 0 {
			resolved, err := argumentSourcePath(path)
			if err != nil {
				return nil, err
			}

			if !selected[resolved] {
				continue
			}
		}

		contents, err := pass.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read argument source: %w", err)
		}

		layout := argumentLayout{pass: pass, contents: contents}
		occurrences := make(map[string]int)
		var layoutError error
		ast.Inspect(file, func(node ast.Node) bool {
			if layoutError != nil {
				return false
			}

			switch node.(type) {
			case *ast.CallExpr, *ast.FuncType:
			default:
				return true
			}

			identity, err := ruleFingerprint(node)
			if err != nil {
				layoutError = err

				return false
			}

			occurrences[identity]++
			list, err := layout.list(node)
			if err != nil {
				layoutError = err

				return false
			}

			edits := layout.edits(list)
			if len(edits) > 0 {
				pass.Report(analysis.Diagnostic{
					Pos: list.opening,
					Message: fmt.Sprintf(
						"argumentlines: separate wrapped arguments or callback parameters [id=%s-%d]",
						identity,
						occurrences[identity],
					),
					SuggestedFixes: []analysis.SuggestedFix{{Message: "Separate list elements", TextEdits: edits}},
				})
			}

			return true
		})
		if layoutError != nil {
			return nil, layoutError
		}
	}

	return nil, nil
}

type argumentElement struct {
	start      token.Pos
	end        token.Pos
	annotation string
}

type argumentLayout struct {
	pass     *analysis.Pass
	contents []byte
}

type argumentList struct {
	opening, closing token.Pos
	elements         []argumentElement
	force            bool
}

func (layout argumentLayout) list(node ast.Node) (argumentList, error) {
	switch typed := node.(type) {
	case *ast.CallExpr:
		if len(typed.Args) < minimumWrappedArguments {
			break
		}

		elements := make([]argumentElement, 0, len(typed.Args))

		for _, value := range typed.Args {
			elements = append(elements, argumentElement{start: value.Pos(), end: value.End()})
		}

		if typed.Ellipsis.IsValid() {
			elements[len(elements)-1].end = typed.Ellipsis + token.Pos(len("..."))
		}

		return argumentList{opening: typed.Lparen, closing: typed.Rparen, elements: elements}, nil
	case *ast.FuncType:
		if typed.Params == nil {
			break
		}

		elements := make([]argumentElement, 0, typed.Params.NumFields())
		callback := false

		for _, field := range typed.Params.List {
			expression := ast.Unparen(field.Type)
			if variadic, ok := expression.(*ast.Ellipsis); ok {
				expression = ast.Unparen(variadic.Elt)
			}

			value := layout.pass.TypesInfo.TypeOf(expression)
			if value == nil {
				return argumentList{}, errArgumentType
			}

			_, function := value.Underlying().(*types.Signature)
			callback = callback || function
			if len(field.Names) == 0 {
				elements = append(elements, argumentElement{start: field.Pos(), end: field.End()})
			}

			for index, name := range field.Names {
				element := argumentElement{start: name.Pos(), end: field.End()}
				if index < len(field.Names)-1 {
					element.end = name.End()
					element.annotation = " " + string(layout.gap(field.Type.Pos(), field.Type.End()))
				}

				elements = append(elements, element)
			}
		}

		return argumentList{
			opening: typed.Params.Opening, closing: typed.Params.Closing, elements: elements, force: callback,
		}, nil
	}

	return argumentList{}, nil
}

func (layout argumentLayout) gap(start, end token.Pos) []byte {
	file := layout.pass.Fset.File(start)

	return layout.contents[file.Offset(start):file.Offset(end)]
}

func (layout argumentLayout) edits(list argumentList) []analysis.TextEdit {
	opening, closing, elements, force := list.opening, list.closing, list.elements, list.force
	if len(elements) == 0 {
		return nil
	}

	previous := opening + 1
	wrapped := force

	for _, element := range elements {
		wrapped = wrapped || bytes.ContainsRune(layout.gap(previous, element.start), '\n')
		previous = element.end
	}

	wrapped = wrapped || bytes.ContainsRune(layout.gap(previous, closing), '\n')
	if !wrapped {
		return nil
	}

	file := layout.pass.Fset.File(opening)
	insertions := make(map[token.Pos]string)
	previous = opening

	for _, element := range elements {
		if file.Line(previous) == file.Line(element.start) {
			insertions[element.start] += "\n"
		}

		insertions[element.end] += element.annotation
		previous = element.end
	}

	if file.Line(previous) == file.Line(closing) {
		if !argumentComma(layout.gap(previous, closing)) {
			insertions[previous] += ","
		}

		insertions[closing] += "\n"
	}

	positions := make([]token.Pos, 0, len(insertions))
	for position, text := range insertions {
		if text != "" {
			positions = append(positions, position)
		}
	}

	slices.Sort(positions)

	edits := make([]analysis.TextEdit, 0, len(positions))
	for _, position := range positions {
		edits = append(edits, analysis.TextEdit{Pos: position, End: position, NewText: []byte(insertions[position])})
	}

	return edits
}

func argumentComma(contents []byte) bool {
	contents = bytes.TrimSpace(contents)
	for bytes.HasPrefix(contents, []byte("/*")) {
		_, after, found := bytes.Cut(contents, []byte("*/"))
		if !found {
			return false
		}

		contents = bytes.TrimSpace(after)
	}

	return bytes.HasPrefix(contents, []byte(","))
}
