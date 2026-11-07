package staticcheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
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

	digest := sha256.Sum256(rendered.Bytes())

	return hex.EncodeToString(digest[:16]), nil
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
	declaration := declarationFor(file, node)
	var scope ast.Node = declaration
	if function, ok := declaration.(*ast.FuncDecl); ok {
		signature := *function
		signature.Doc = nil
		scope = &signature
	}

	if scope == nil {
		scope = file.Name
	}

	parent, err := nodeFingerprint(scope)
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
		return "", fmt.Errorf("cannot locate a formatting finding in its declaration")
	}

	digest := sha256.Sum256([]byte(key))

	return fmt.Sprintf("%x-%d", digest[:16], count), nil
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
