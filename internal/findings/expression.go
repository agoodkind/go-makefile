package findings

import (
	"bytes"
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

	var source bytes.Buffer
	if err := format.Node(&source, token.NewFileSet(), parsed); err != nil {
		return diagnostic
	}

	return prefix + errorExpressionMarker + strconv.Quote(source.String()) + " (err113)"
}
