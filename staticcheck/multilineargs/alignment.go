package multilineargs

import (
	"bytes"
	"go/ast"
	"go/token"
)

func (planner *layoutPlanner) reserveDeclarationColumns(declaration *ast.GenDecl) {
	if !declaration.Lparen.IsValid() ||
		(declaration.Tok != token.VAR && declaration.Tok != token.CONST) {
		return
	}

	start := 0

	for index := 1; index < len(declaration.Specs); index++ {
		previous := planner.tokens.Line(declaration.Specs[index-1].End())
		current := planner.tokens.Line(declaration.Specs[index].Pos())
		if current > previous+1 {
			planner.reserveDeclarationGroup(declaration.Specs[start:index])
			start = index
		}
	}

	planner.reserveDeclarationGroup(declaration.Specs[start:])
}

func (planner *layoutPlanner) reserveDeclarationGroup(specifications []ast.Spec) {
	column := 0

	for _, specification := range specifications {
		value, valid := specification.(*ast.ValueSpec)
		if valid && len(value.Values) > 0 {
			column = max(column, planner.column(value.Values[0].Pos()))
		}
	}

	for _, specification := range specifications {
		value, valid := specification.(*ast.ValueSpec)
		if !valid || len(value.Values) == 0 {
			continue
		}

		start := planner.tokens.Offset(value.Values[0].Pos())
		for _, expression := range value.Values {
			prefix := planner.source[start:planner.tokens.Offset(expression.Pos())]
			if bytes.IndexByte(prefix, '\n') < 0 {
				planner.reserveColumns(expression, column+displayWidth(string(prefix)))
			}
		}
	}
}

func (planner *layoutPlanner) reserveColumns(expression ast.Expr, column int) {
	start := planner.tokens.Offset(expression.Pos())
	ast.Inspect(
		expression,
		func(node ast.Node) bool {
			if node == nil {
				return false
			}

			prefix := planner.source[start:planner.tokens.Offset(node.Pos())]
			if bytes.IndexByte(prefix, '\n') >= 0 {
				return false
			}

			planner.columns[node.Pos()] = max(
				planner.columns[node.Pos()],
				column+displayWidth(string(prefix)),
			)

			return true
		},
	)
}
