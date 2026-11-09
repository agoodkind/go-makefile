package staticcheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
)

const ruleHashBytes = 16

func ruleFingerprint(node ast.Node) (string, error) {
	var source bytes.Buffer
	if err := format.Node(&source, token.NewFileSet(), node); err != nil {
		return "", fmt.Errorf("identify source occurrence: %w", err)
	}

	if _, declaration := node.(*ast.FuncDecl); declaration {
		file, err := parser.ParseFile(
			token.NewFileSet(),
			"identity.go",
			"package identity\n"+source.String(),
			parser.ParseComments|parser.SkipObjectResolution,
		)
		if err != nil {
			return "", fmt.Errorf("parse source identity: %w", err)
		}

		ast.Inspect(file, expandIdentityParameters)
		source.Reset()
		if err := format.Node(&source, token.NewFileSet(), file.Decls[0]); err != nil {
			return "", fmt.Errorf("normalize source identity: %w", err)
		}
	}

	digest := sha256.Sum256(source.Bytes())

	return hex.EncodeToString(digest[:ruleHashBytes]), nil
}

func expandIdentityParameters(node ast.Node) bool {
	function, valid := node.(*ast.FuncType)
	if !valid {
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
				expandedField := &ast.Field{Names: []*ast.Ident{name}, Type: field.Type}
				if index == 0 {
					expandedField.Doc = field.Doc
				}

				if index == len(field.Names)-1 {
					expandedField.Comment = field.Comment
				}

				expanded = append(expanded, expandedField)
			}
		}

		fields.List = expanded
	}

	return true
}
