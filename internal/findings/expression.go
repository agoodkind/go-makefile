package findings

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

const errorExpressionMarker = "do not define dynamic errors, use wrapped static errors instead: "

// ErrorExpressionKey normalizes the source expression in an err113 diagnostic.
//
// Source whitespace does not affect the key; literal contents remain exact.
//
// For example, wrapped calls retain their key:
//
//	fmt.Errorf("message", value)
func ErrorExpressionKey(diagnostic string) string {
	if !strings.HasSuffix(diagnostic, " (err113)") {
		return diagnostic
	}

	prefix, quoted, found := strings.Cut(diagnostic, errorExpressionMarker)
	if !found {
		return diagnostic
	}

	expression, err := strconv.Unquote(strings.TrimSuffix(quoted, " (err113)"))
	if err != nil {
		return diagnostic
	}

	parsed, err := parser.ParseExpr(expression)
	if err != nil {
		return diagnostic
	}

	ast.Inspect(
		parsed,
		func(node ast.Node) bool {
			call, valid := node.(*ast.CallExpr)
			if !valid {
				return true
			}

			for index, argument := range call.Args {
				if value, literal := stringLiteral(argument); literal {
					call.Args[index] = &ast.BasicLit{
						Kind:  token.STRING,
						Value: strconv.Quote(value),
					}
				}
			}

			return true
		},
	)
	var source bytes.Buffer
	if err := format.Node(&source, token.NewFileSet(), parsed); err != nil {
		return diagnostic
	}

	return prefix + errorExpressionMarker + strconv.Quote(source.String()) + " (err113)"
}

func stringLiteral(expression ast.Expr) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}

		text, err := strconv.Unquote(value.Value)

		return text, err == nil
	case *ast.ParenExpr:
		return stringLiteral(value.X)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}

		left, leftLiteral := stringLiteral(value.X)
		right, rightLiteral := stringLiteral(value.Y)

		return left + right, leftLiteral && rightLiteral
	default:
		return "", false
	}
}
