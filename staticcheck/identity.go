package staticcheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
)

var (
	errFormattingDeclarationMissing = errors.New("cannot locate the declaration containing a formatting finding")
	errFormattingNodeMissing        = errors.New("cannot locate a formatting finding in its declaration")
)

func nodeFingerprint(node ast.Node) (string, error) {
	switch declaration := node.(type) {
	case *ast.FuncDecl:
		declarationCopy := *declaration
		declarationCopy.Doc = nil
		node = &declarationCopy
	case *ast.GenDecl:
		declarationCopy := *declaration
		declarationCopy.Doc = nil
		node = &declarationCopy
	}

	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), node); err != nil {
		return "", fmt.Errorf("identify Go source: %w", err)
	}

	if hasGroupedParameters(node) {
		if err := expandFingerprintParameters(node, &rendered); err != nil {
			return "", err
		}
	}

	digest := sha256.Sum256(rendered.Bytes())

	return hex.EncodeToString(digest[:16]), nil
}

func hasGroupedParameters(node ast.Node) bool {
	for child := range ast.Preorder(node) {
		signature, ok := child.(*ast.FuncType)
		if !ok || signature.Params == nil {
			continue
		}

		for _, field := range signature.Params.List {
			if len(field.Names) > 1 {
				return true
			}
		}
	}

	return false
}

// expandFingerprintParameters normalizes a copy of the input syntax tree.
func expandFingerprintParameters(original ast.Node, rendered *bytes.Buffer) error {
	var node ast.Node
	if _, declaration := original.(ast.Decl); declaration {
		file, err := parser.ParseFile(token.NewFileSet(), "", "package p\n"+rendered.String(), parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse declaration fingerprint: %w", err)
		}

		node = file.Decls[0]
	} else {
		expression, err := parser.ParseExpr(rendered.String())
		if err != nil {
			return fmt.Errorf("parse expression fingerprint: %w", err)
		}

		node = expression
	}

	for child := range ast.Preorder(node) {
		signature, ok := child.(*ast.FuncType)
		if !ok || signature.Params == nil {
			continue
		}

		var fields []*ast.Field

		for _, field := range signature.Params.List {
			if len(field.Names) <= 1 {
				fields = append(fields, field)

				continue
			}

			for _, name := range field.Names {
				parameter := *field
				parameter.Names = []*ast.Ident{name}
				fields = append(fields, &parameter)
			}
		}

		signature.Params.List = fields
	}

	rendered.Reset()
	if err := format.Node(rendered, token.NewFileSet(), node); err != nil {
		return fmt.Errorf("render declaration fingerprint: %w", err)
	}

	return nil
}

func declarationFor(file *ast.File, node ast.Node) ast.Decl {
	for _, declaration := range file.Decls {
		if declaration.Pos() <= node.Pos() && node.End() <= declaration.End() {
			return declaration
		}
	}

	return nil
}

func declarationName(declaration ast.Decl) string {
	switch typed := declaration.(type) {
	case *ast.FuncDecl:
		return typed.Name.Name
	case *ast.GenDecl:
		for _, specification := range typed.Specs {
			switch named := specification.(type) {
			case *ast.TypeSpec:
				return named.Name.Name
			case *ast.ValueSpec:
				if len(named.Names) > 0 {
					return named.Names[0].Name
				}
			}
		}
	}

	return "package initializer"
}

func occurrenceIdentity(file *ast.File, node ast.Node) (string, error) {
	scope := declarationFor(file, node)
	if scope == nil {
		return "", errFormattingDeclarationMissing
	}

	parent, err := declarationIdentity(file, scope)
	if err != nil {
		return "", err
	}

	fingerprint, err := nodeFingerprint(node)
	if err != nil {
		return "", err
	}

	key := parent + ":" + fingerprint
	count := 0
	found := false
	var identityErr error
	ast.Inspect(scope, func(candidate ast.Node) bool {
		if candidate == nil || found || identityErr != nil {
			return false
		}

		if !sameLayoutKind(candidate, node) {
			return true
		}

		identity, err := nodeFingerprint(candidate)
		if err != nil {
			identityErr = err

			return false
		}

		if identity == fingerprint {
			count++
		}

		if candidate == node {
			found = true

			return false
		}

		return true
	})
	if identityErr != nil {
		return "", identityErr
	}

	if !found {
		return "", errFormattingNodeMissing
	}

	digest := sha256.Sum256([]byte(key))

	return fmt.Sprintf("%x-%d", digest[:16], count), nil
}

func declarationIdentity(file *ast.File, scope ast.Decl) (string, error) {
	fingerprint, err := nodeFingerprint(scope)
	if err != nil {
		return "", err
	}

	name := declarationName(scope)
	ordinal := 1

	for _, prior := range file.Decls {
		if prior == scope {
			break
		}

		if declarationName(prior) != name {
			continue
		}

		identity, err := nodeFingerprint(prior)
		if err != nil {
			return "", err
		}

		if identity == fingerprint {
			ordinal++
		}
	}

	if ordinal == 1 {
		return fingerprint, nil
	}

	return fmt.Sprintf("%s:%d", fingerprint, ordinal), nil
}

func sameLayoutKind(first, second ast.Node) bool {
	switch first.(type) {
	case *ast.CallExpr:
		_, ok := second.(*ast.CallExpr)

		return ok
	case *ast.FuncType:
		_, ok := second.(*ast.FuncType)

		return ok
	}

	return false
}
