package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// CommandEntryAnalyzer requires command packages to delegate implementation.
var CommandEntryAnalyzer = &analysis.Analyzer{
	Name: "commandentry",
	Doc:  "requires cmd entry points to delegate execution to an imported package",
	Run:  runCommandEntry,
}

func runCommandEntry(pass *analysis.Pass) (any, error) {
	packagePath := pass.Pkg.Path()
	if pass.Module != nil {
		packagePath = strings.TrimPrefix(packagePath, pass.Module.Path)
	}

	if !strings.Contains("/"+packagePath+"/", "/cmd/") {
		return nil, nil
	}

	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file, path) || isGeneratedTestMainFile(file, path) {
			continue
		}

		counts := make(map[string]int)

		for _, declaration := range file.Decls {
			if imports, ok := declaration.(*ast.GenDecl); ok && imports.Tok == token.IMPORT {
				continue
			}

			if function, ok := declaration.(*ast.FuncDecl); ok && delegatingEntry(pass, function) {
				continue
			}

			identity, err := nodeFingerprint(declaration)
			if err != nil {
				return nil, err
			}

			counts[identity]++
			reportAtf(pass, file, declaration.Pos(), "commandentry: %s contains implementation in cmd; delegate to an importable package [id=%s-%d]", declarationName(declaration), identity, counts[identity])
		}
	}

	return nil, nil
}

func delegatingEntry(pass *analysis.Pass, function *ast.FuncDecl) bool {
	if function.Recv != nil || function.Body == nil || len(function.Body.List) != 1 {
		return false
	}

	var expression ast.Expr
	switch statement := function.Body.List[0].(type) {
	case *ast.ExprStmt:
		expression = statement.X
	case *ast.ReturnStmt:
		if len(statement.Results) != 1 {
			return false
		}

		expression = statement.Results[0]
	default:
		return false
	}

	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}

	if importedCall(pass, call, "os", "Exit") {
		if len(call.Args) != 1 {
			return false
		}

		call, ok = call.Args[0].(*ast.CallExpr)
		if !ok {
			return false
		}
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || pass.TypesInfo == nil {
		return false
	}

	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}

	imported, ok := pass.TypesInfo.Uses[identifier].(*types.PkgName)
	if !ok || !applicationImport(pass, imported.Imported().Path()) {
		return false
	}

	valid := true

	for _, argument := range call.Args {
		ast.Inspect(argument, func(node ast.Node) bool {
			switch node.(type) {
			case *ast.CallExpr, *ast.FuncLit, *ast.CompositeLit, *ast.BinaryExpr, *ast.UnaryExpr:
				valid = false
				return false
			}

			return true
		})
	}

	return valid
}

func applicationImport(pass *analysis.Pass, path string) bool {
	first, _, _ := strings.Cut(path, "/")
	if strings.Contains(first, ".") {
		return true
	}

	return pass.Module != nil && (path == pass.Module.Path || strings.HasPrefix(path, pass.Module.Path+"/"))
}

func importedCall(pass *analysis.Pass, call *ast.CallExpr, path, name string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name || pass.TypesInfo == nil {
		return false
	}

	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}

	imported, ok := pass.TypesInfo.Uses[identifier].(*types.PkgName)

	return ok && imported.Imported().Path() == path
}
