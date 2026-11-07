package staticcheck

import (
	"bytes"
	"go/ast"
	"go/token"
	"slices"

	"golang.org/x/tools/go/analysis"
)

type listElement struct {
	start      token.Pos
	end        token.Pos
	annotation string
}

func parameterLineEdits(fileSet *token.FileSet, function *ast.FuncType, contents []byte) []analysis.TextEdit {
	if function.Params == nil {
		return nil
	}

	file := fileSet.File(function.Pos())
	elements := make([]listElement, 0, function.Params.NumFields())
	callback := false

	for _, field := range function.Params.List {
		if inlineCallback(field.Type) {
			callback = true
		}

		if len(field.Names) == 0 {
			elements = append(elements, listElement{start: field.Pos(), end: field.End()})
			continue
		}

		for index, name := range field.Names {
			element := listElement{start: name.Pos(), end: field.End()}
			if index < len(field.Names)-1 {
				element.end = name.End()
				element.annotation = " " + string(contents[file.Offset(field.Type.Pos()):file.Offset(field.Type.End())])
			}

			elements = append(elements, element)
		}
	}

	return listLineEdits(fileSet, function.Params.Opening, function.Params.Closing, elements, contents, callback)
}

func inlineCallback(expression ast.Expr) bool {
	switch typed := expression.(type) {
	case *ast.FuncType:
		return true
	case *ast.ParenExpr:
		return inlineCallback(typed.X)
	case *ast.Ellipsis:
		return inlineCallback(typed.Elt)
	}

	return false
}

func listLineEdits(fileSet *token.FileSet, opening, closing token.Pos, elements []listElement, contents []byte, force bool) []analysis.TextEdit {
	if len(elements) == 0 {
		return nil
	}

	file := fileSet.File(opening)
	gap := func(start, end token.Pos) []byte {
		return contents[file.Offset(start):file.Offset(end)]
	}
	previous := opening + 1
	wrapped := force

	for _, element := range elements {
		if bytes.ContainsRune(gap(previous, element.start), '\n') {
			wrapped = true
		}

		previous = element.end
	}

	if bytes.ContainsRune(gap(previous, closing), '\n') {
		wrapped = true
	}

	if !wrapped {
		return nil
	}

	insertions := make(map[token.Pos]string)
	previous = opening

	for _, element := range elements {
		if file.Line(previous) == file.Line(element.start) {
			insertions[element.start] += "\n"
		}

		if element.annotation != "" {
			insertions[element.end] += element.annotation
		}

		previous = element.end
	}

	if file.Line(previous) == file.Line(closing) {
		if !argumentTrailingComma(gap(previous, closing)) {
			insertions[previous] += ","
		}

		insertions[closing] += "\n"
	}

	positions := make([]token.Pos, 0, len(insertions))
	for position := range insertions {
		positions = append(positions, position)
	}

	slices.Sort(positions)

	edits := make([]analysis.TextEdit, 0, len(positions))
	for _, position := range positions {
		edits = append(edits, analysis.TextEdit{Pos: position, End: position, NewText: []byte(insertions[position])})
	}

	return edits
}
