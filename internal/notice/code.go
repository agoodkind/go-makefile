package notice

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"reflect"
	"strconv"
)

func (parsed Source) fingerprint(nodes []ast.Node) (string, error) {
	var encoder syntaxEncoder

	for index, node := range nodes {
		if err := encoder.value(
			reflect.ValueOf(node),
			"node["+strconv.Itoa(index)+"]",
		); err != nil {
			return "", err
		}
	}

	comments, err := parsed.comments(nodes, encoder.positions)
	if err != nil {
		return "", err
	}

	if err := encoder.value(reflect.ValueOf(comments), "comments"); err != nil {
		return "", err
	}

	digest := sha256.Sum256([]byte(encoder.contents.String()))

	return hex.EncodeToString(digest[:]), nil
}

func expandParameters(node ast.Node) bool {
	function, ok := node.(*ast.FuncType)
	if !ok {
		return true
	}

	for _, fields := range []*ast.FieldList{function.Params, function.Results} {
		if fields == nil {
			continue
		}

		var expanded []*ast.Field

		for _, field := range fields.List {
			if len(field.Names) <= 1 {
				expanded = append(expanded, field)

				continue
			}

			for index, name := range field.Names {
				expandedField := *field
				expandedField.Names = []*ast.Ident{name}
				if index > 0 {
					expandedField.Doc = nil
				}

				if index < len(field.Names)-1 {
					expandedField.Comment = nil
				}

				expanded = append(expanded, &expandedField)
			}
		}

		fields.List = expanded
	}

	return true
}
