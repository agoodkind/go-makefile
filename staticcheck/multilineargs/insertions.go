package multilineargs

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"slices"
	"strings"
)

const (
	maximumLineWidth = 100
	// golines counts each tab as four columns rather than using tab stops.
	tabWidth            = 4
	maximumLayoutRounds = 20
	variadicMarker      = "..."
	compactFilename     = "compact-expression"
	newlineText         = "\n"
	commaText           = ","
	indentText          = "\t"
)

type layoutEdit struct {
	start int
	end   int
	text  string
}

var errOverlappingEdits = errors.New("overlapping source layout edits")

type layoutPlanner struct {
	file    *ast.File
	tokens  *token.File
	source  []byte
	edits   []layoutEdit
	forced  map[*ast.CompositeLit]bool
	columns map[token.Pos]int
	err     error
}

func (planner *layoutPlanner) visit(node ast.Node) bool {
	if planner.err != nil {
		return false
	}

	switch value := node.(type) {
	case *ast.CallExpr:
		return planner.call(value)
	case *ast.FuncType:
		planner.fields(value.Params)
		planner.fields(value.Results)
	case *ast.CompositeLit:
		planner.literal(value)
	case *ast.GenDecl:
		planner.reserveDeclarationColumns(value)
	}

	return true
}

func (planner *layoutPlanner) call(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return true
	}

	if planner.simple(call) || planner.compactLiteralCall(call) {
		compact, err := compactExpression(planner.fragment(call))
		if err != nil {
			planner.err = err

			return false
		}

		if planner.callFitsLine(call, compact) {
			planner.replace(call.Pos(), call.End(), compact)

			return false
		}
	}

	if len(call.Args) == 1 && planner.hug(call) {
		return true
	}

	content := planner.fragment(call)
	if !strings.Contains(content, newlineText) &&
		planner.callFitsLine(call, content) {
		return true
	}

	planner.expressions(call.Lparen, call.Rparen, call.Args, call.Ellipsis)

	return true
}

// Include text after a call when checking its line width.
func (planner *layoutPlanner) callFitsLine(call *ast.CallExpr, content string) bool {
	suffix := planner.source[planner.tokens.Offset(call.End()):]
	if end := bytes.IndexByte(suffix, '\n'); end >= 0 {
		suffix = suffix[:end]
	}

	callWidth := planner.column(call.Pos()) + displayWidth(content)

	return callWidth+displayWidth(string(suffix)) <= maximumLineWidth
}

func (planner *layoutPlanner) compactLiteralCall(call *ast.CallExpr) bool {
	if len(call.Args) != 1 || planner.hasComments(call.Pos(), call.End()) {
		return false
	}

	argument := call.Args[0]
	if unary, ok := argument.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		argument = unary.X
	}

	literal, ok := argument.(*ast.CompositeLit)
	if !ok || !planner.simple(call.Fun) || (literal.Type != nil && !planner.simple(literal.Type)) {
		return false
	}

	if sequenceLiteral(literal) {
		if strings.Contains(planner.fragment(literal), newlineText) {
			return false
		}
	} else if len(literal.Elts) > 1 {
		return false
	}

	for _, element := range literal.Elts {
		if !planner.simple(element) {
			return false
		}
	}

	return true
}

func sequenceLiteral(literal *ast.CompositeLit) bool {
	switch literal.Type.(type) {
	case *ast.ArrayType, *ast.MapType:
		return true
	default:
		return false
	}
}

func (planner *layoutPlanner) simple(expression ast.Expr) bool {
	if planner.hasComments(expression.Pos(), expression.End()) {
		return false
	}

	simple := true
	ast.Inspect(
		expression,
		func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CompositeLit,
				*ast.FuncLit,
				*ast.FuncType,
				*ast.StructType,
				*ast.InterfaceType:
				simple = false
			case *ast.BasicLit:
				if strings.Contains(value.Value, newlineText) {
					simple = false
				}
			}

			return simple
		},
	)

	return simple
}

func (planner *layoutPlanner) hug(call *ast.CallExpr) bool {
	argument := call.Args[0]
	structured := argument
	if unary, ok := structured.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		structured = unary.X
	}

	var opening token.Pos
	switch value := structured.(type) {
	case *ast.CompositeLit:
		if (!sequenceLiteral(value) && len(value.Elts) > 1) ||
			strings.Contains(planner.fragment(value), newlineText) ||
			!planner.callFitsLine(call, planner.fragment(call)) {
			planner.forced[value] = true
		}

		opening = value.Lbrace
	case *ast.FuncLit:
		content := planner.fragment(call.Fun) + "(" + planner.fragment(argument) + ")"
		if !strings.Contains(planner.fragment(value), newlineText) &&
			!planner.callFitsLine(call, content) {
			return false
		}

		opening = value.Body.Lbrace
	default:
		return false
	}

	if planner.hasComments(call.Lparen+1, argument.Pos()) ||
		planner.hasComments(argument.End(), call.Rparen) {
		return false
	}

	prefix := string(
		planner.source[planner.tokens.Offset(argument.Pos()) : planner.tokens.Offset(opening)+1],
	)
	if strings.Contains(prefix, newlineText) ||
		planner.column(call.Lparen)+1+displayWidth(prefix) > maximumLineWidth {
		return false
	}

	planner.replace(call.Lparen+1, argument.Pos(), "")
	ending := ""
	if call.Ellipsis.IsValid() {
		ending = variadicMarker
	}

	planner.replace(argument.End(), call.Rparen, ending)

	return true
}

func (planner *layoutPlanner) expressions(
	opening token.Pos,
	closing token.Pos,
	expressions []ast.Expr,
	ellipsis token.Pos,
) {
	if len(expressions) == 0 {
		return
	}

	indent := planner.indent(opening)

	for _, expression := range expressions {
		planner.newline(expression.Pos(), indent+indentText)
	}

	planner.newline(closing, indent)
	end := expressions[len(expressions)-1].End()
	if ellipsis.IsValid() {
		end = ellipsis + token.Pos(len(variadicMarker))
	}

	planner.trailingComma(end, closing)
}

func (planner *layoutPlanner) literal(literal *ast.CompositeLit) {
	if len(literal.Elts) == 0 {
		return
	}

	content := planner.fragment(literal)
	if !planner.forced[literal] && !strings.Contains(content, newlineText) &&
		planner.column(literal.Pos())+displayWidth(content) <= maximumLineWidth &&
		!planner.hasComments(literal.Pos(), literal.End()) {
		return
	}

	planner.reserveLiteralColumns(literal)
	planner.expressions(literal.Lbrace, literal.Rbrace, literal.Elts, token.NoPos)
}

// Reserve the padding gofmt adds when a multiline value becomes compact.
func (planner *layoutPlanner) reserveLiteralColumns(literal *ast.CompositeLit) {
	longest := 0

	for _, element := range literal.Elts {
		if field, ok := element.(*ast.KeyValueExpr); ok {
			longest = max(longest, displayWidth(planner.fragment(field.Key)))
		}
	}

	column := displayWidth(planner.indent(literal.Lbrace)) + tabWidth + longest + len(": ")
	for _, element := range literal.Elts {
		if field, ok := element.(*ast.KeyValueExpr); ok {
			planner.reserveColumns(field.Value, column)
		}
	}
}

func (planner *layoutPlanner) fields(fields *ast.FieldList) {
	if fields == nil || !fields.Opening.IsValid() || len(fields.List) == 0 {
		return
	}

	content := string(
		planner.source[planner.tokens.Offset(
			fields.Opening,
		) : planner.tokens.Offset(fields.Closing)+1],
	)
	if !strings.Contains(content, newlineText) &&
		planner.column(fields.Opening)+displayWidth(content) <= maximumLineWidth {
		return
	}

	indent := planner.indent(fields.Opening)
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			planner.newline(field.Type.Pos(), indent+indentText)

			continue
		}

		for index, name := range field.Names {
			planner.newline(name.Pos(), indent+indentText)
			if index < len(field.Names)-1 {
				planner.insert(name.End(), " "+planner.typeWithoutComments(field.Type))
			}
		}
	}

	planner.newline(fields.Closing, indent)
	planner.trailingComma(fields.List[len(fields.List)-1].End(), fields.Closing)
}

func (planner *layoutPlanner) typeWithoutComments(expression ast.Expr) string {
	start, end := planner.tokens.Offset(expression.Pos()), planner.tokens.Offset(expression.End())

	content := bytes.Clone(planner.source[start:end])
	for _, group := range planner.file.Comments {
		for _, comment := range group.List {
			left, right := planner.tokens.Offset(comment.Pos()), planner.tokens.Offset(
				comment.End(),
			)
			if left < start || right > end {
				continue
			}

			for index := left - start; index < right-start; index++ {
				if content[index] != '\n' {
					content[index] = ' '
				}
			}
		}
	}

	return string(content)
}

func (planner *layoutPlanner) hasComments(start, end token.Pos) bool {
	for _, group := range planner.file.Comments {
		if group.Pos() < end && group.End() > start {
			return true
		}
	}

	return false
}

func (planner *layoutPlanner) fragment(node ast.Node) string {
	return string(
		planner.source[planner.tokens.Offset(node.Pos()):planner.tokens.Offset(node.End())],
	)
}

func (planner *layoutPlanner) trailingComma(end, closing token.Pos) {
	fragment := planner.source[planner.tokens.Offset(end):planner.tokens.Offset(closing)]
	if !containsComma(fragment) {
		planner.insert(end, commaText)
	}
}

func containsComma(source []byte) bool {
	files := token.NewFileSet()
	var lexer scanner.Scanner
	lexer.Init(files.AddFile(compactFilename, -1, len(source)), source, nil, scanner.ScanComments)

	for {
		_, kind, _ := lexer.Scan()
		if kind == token.EOF {
			return false
		}

		if kind == token.COMMA {
			return true
		}
	}
}

func compactExpression(source string) (string, error) {
	files := token.NewFileSet()
	var lexer scanner.Scanner
	lexer.Init(files.AddFile(compactFilename, -1, len(source)), []byte(source), nil, 0)
	var pieces []string

	for {
		_, kind, literal := lexer.Scan()
		if kind == token.EOF {
			break
		}

		if kind == token.SEMICOLON && literal == newlineText {
			continue
		}

		if literal == "" {
			literal = kind.String()
		}

		pieces = append(pieces, literal)
	}

	parsed, err := parser.ParseExprFrom(
		files,
		compactFilename,
		strings.Join(pieces, " "),
		parser.SkipObjectResolution,
	)
	if err != nil {
		return "", fmt.Errorf("parse compact expression: %w", err)
	}

	var output bytes.Buffer
	if err := format.Node(&output, files, parsed); err != nil {
		return "", fmt.Errorf("format compact expression: %w", err)
	}

	return output.String(), nil
}

func (planner *layoutPlanner) replace(start, end token.Pos, text string) {
	left, right := planner.tokens.Offset(start), planner.tokens.Offset(end)
	if string(planner.source[left:right]) == text {
		return
	}

	if left == right {
		planner.insert(start, text)

		return
	}

	planner.edits = append(planner.edits, layoutEdit{start: left, end: right, text: text})
}

func (planner *layoutPlanner) insert(position token.Pos, text string) {
	offset := planner.tokens.Offset(position)
	for index := range planner.edits {
		edit := &planner.edits[index]
		if edit.start != offset || edit.end != offset {
			continue
		}

		if text == commaText {
			if !strings.HasPrefix(edit.text, commaText) {
				edit.text = commaText + edit.text
			}
		} else if !strings.Contains(edit.text, text) {
			edit.text += text
		}

		return
	}

	planner.edits = append(planner.edits, layoutEdit{start: offset, end: offset, text: text})
}

func (planner *layoutPlanner) newline(position token.Pos, indent string) {
	offset := planner.tokens.Offset(position)
	start := bytes.LastIndexByte(planner.source[:offset], '\n') + 1
	if len(bytes.TrimSpace(planner.source[start:offset])) != 0 {
		planner.insert(position, newlineText+indent)
	}
}

func (planner *layoutPlanner) projectedPrefix(position token.Pos) string {
	offset := planner.tokens.Offset(position)

	start := bytes.LastIndexByte(planner.source[:offset], '\n') + 1
	for _, edit := range planner.edits {
		if edit.start < start && edit.end >= start && edit.end <= offset {
			start = bytes.LastIndexByte(planner.source[:edit.start], '\n') + 1
		}
	}

	var edits []layoutEdit
	for _, edit := range planner.edits {
		if edit.start >= start && edit.end <= offset {
			edits = append(
				edits,
				layoutEdit{start: edit.start - start, end: edit.end - start, text: edit.text},
			)
		}
	}

	result, err := applyLayoutEdits(planner.source[start:offset], edits)
	if err != nil {
		planner.err = err

		return ""
	}

	return string(result[bytes.LastIndexByte(result, '\n')+1:])
}

func (planner *layoutPlanner) column(position token.Pos) int {
	return max(displayWidth(planner.projectedPrefix(position)), planner.columns[position])
}

func (planner *layoutPlanner) indent(position token.Pos) string {
	prefix := planner.projectedPrefix(position)

	return prefix[:len(prefix)-len(strings.TrimLeft(prefix, " \t"))]
}

func displayWidth(text string) int {
	width := 0

	for _, character := range text {
		if character == '\t' {
			width += tabWidth
		} else {
			width++
		}
	}

	return width
}

func applyLayoutEdits(source []byte, edits []layoutEdit) ([]byte, error) {
	slices.SortStableFunc(
		edits,
		func(left, right layoutEdit) int {
			if left.start != right.start {
				return left.start - right.start
			}

			return left.end - right.end
		},
	)
	var output []byte
	previous := 0

	for _, edit := range edits {
		if edit.start < previous {
			return nil, fmt.Errorf("%w at byte %d", errOverlappingEdits, edit.start)
		}

		output = append(output, source[previous:edit.start]...)
		output = append(output, edit.text...)
		previous = edit.end
	}

	return append(output, source[previous:]...), nil
}
