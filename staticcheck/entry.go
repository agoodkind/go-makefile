package staticcheck

import (
	"go/ast"
	"go/build"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// CommandEntryAnalyzer requires command packages to delegate implementation.
//
// Command functions may handle errors, log events, and select exit statuses.
// The check examines each declaration, including generated declarations.
//
// Keep implementation in an importable package:
//
//	func main() { os.Exit(app.Run()) }
var CommandEntryAnalyzer = &analysis.Analyzer{
	Name: "commandentry",
	Doc:  "require thin command entry points that delegate implementation",
	Run:  runCommandEntry,
}

func runCommandEntry(pass *analysis.Pass) (any, error) {
	path := pass.Pkg.Path()
	if pass.Module != nil {
		path = strings.TrimPrefix(path, pass.Module.Path+"/")
	}

	if !strings.HasPrefix(path, "cmd/") {
		return nil, nil
	}

	for _, file := range pass.Files {
		if isTestFile(fileName(pass, file.Pos())) || isGeneratedTestMainFile(file, fileName(pass, file.Pos())) {
			continue
		}

		occurrences := make(map[string]int)

		for _, declaration := range file.Decls {
			identity, err := ruleFingerprint(declaration)
			if err != nil {
				return nil, err
			}

			occurrences[identity]++

			if !entryDeclaration(pass, declaration) {
				reportAtf(
					pass,
					file,
					declaration.Pos(),
					"commandentry: delegate implementation to an importable package [id=%s:%d]",
					identity,
					occurrences[identity],
				)
			}
		}
	}

	return nil, nil
}

func entryDeclaration(pass *analysis.Pass, declaration ast.Decl) bool {
	if general, ok := declaration.(*ast.GenDecl); ok {
		if general.Tok == token.IMPORT {
			return true
		}

		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || general.Tok != token.CONST {
				return false
			}

			for _, name := range value.Names {
				if !entryInteger(pass.TypesInfo.Defs[name].Type()) {
					return false
				}
			}
		}

		return true
	}

	function, ok := declaration.(*ast.FuncDecl)
	if !ok || function.Recv != nil || function.Body == nil {
		return false
	}

	return entryBlock(pass, function.Body.List, false)
}

func entryBlock(pass *analysis.Pass, statements []ast.Stmt, handling bool) bool {
	for _, statement := range statements {
		if !entryStatement(pass, statement, handling) {
			return false
		}
	}

	return true
}

func entryStatement(pass *analysis.Pass, statement ast.Stmt, handling bool) bool {
	switch node := statement.(type) {
	case *ast.ExprStmt:
		return entryExpression(pass, node.X, handling)
	case *ast.ReturnStmt:
		for _, result := range node.Results {
			if !entryExpression(pass, result, handling) {
				return false
			}
		}

		return true
	case *ast.AssignStmt:
		return entryAssignment(pass, node, handling)
	case *ast.IfStmt:
		return (node.Init == nil || entryStatement(pass, node.Init, false)) &&
			entryCondition(pass, node.Cond) && entryBlock(pass, node.Body.List, true) &&
			(node.Else == nil || entryStatement(pass, node.Else, true))
	case *ast.BlockStmt:
		return entryBlock(pass, node.List, handling)
	case *ast.SwitchStmt:
		if node.Init != nil || node.Tag == nil || !entryExpression(pass, node.Tag, true) {
			return false
		}

		for _, statement := range node.Body.List {
			clause, ok := statement.(*ast.CaseClause)
			if !ok || !entryBlock(pass, clause.Body, true) {
				return false
			}
		}

		return true
	}

	return false
}

func entryAssignment(pass *analysis.Pass, node *ast.AssignStmt, handling bool) bool {
	for _, target := range node.Lhs {
		if _, ok := target.(*ast.Ident); !ok {
			return false
		}
	}

	for _, value := range node.Rhs {
		if _, call := value.(*ast.CallExpr); !call && (!handling || !entryInteger(pass.TypesInfo.TypeOf(value))) {
			return false
		}

		if !entryExpression(pass, value, handling) {
			return false
		}
	}

	return node.Tok == token.DEFINE || node.Tok == token.ASSIGN
}

func entryCondition(pass *analysis.Pass, expression ast.Expr) bool {
	switch node := expression.(type) {
	case *ast.BinaryExpr:
		operators := []token.Token{token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ, token.LAND, token.LOR}

		return slices.Contains(operators, node.Op) && entryCondition(pass, node.X) && entryCondition(pass, node.Y)
	case *ast.UnaryExpr:
		return node.Op == token.NOT && entryCondition(pass, node.X)
	default:
		return entryExpression(pass, expression, true)
	}
}

func entryExpression(pass *analysis.Pass, expression ast.Expr, handling bool) bool {
	switch node := expression.(type) {
	case *ast.Ident, *ast.BasicLit:
		return true
	case *ast.SelectorExpr:
		return entryExpression(pass, node.X, handling)
	case *ast.ParenExpr:
		return entryExpression(pass, node.X, handling)
	case *ast.CallExpr:
		if selector, ok := node.Fun.(*ast.SelectorExpr); ok && !entryExpression(pass, selector.X, false) {
			return false
		}

		function, ok := typeutil.Callee(pass.TypesInfo, node).(*types.Func)
		if !ok || function.Pkg() == nil {
			return false
		}

		path := function.Pkg().Path()
		metadata, err := build.Default.Import(path, "", build.FindOnly)
		standard := err == nil && metadata.Goroot
		if standard || handling {
			name := path + "." + function.Name()
			allowed := " errors.Is errors.As errors.Join fmt.Errorf os.Exit "
			logging := path == "log/slog" && slices.Contains(
				strings.Fields("Debug Info Warn Error Log LogAttrs DebugContext InfoContext WarnContext ErrorContext "+
					"Any String Int Int64 Uint64 Bool Duration Float64 Time Group GroupAttrs"),
				function.Name(),
			)
			if !logging && !strings.Contains(allowed, " "+name+" ") {
				return false
			}
		}

		for _, argument := range node.Args {
			if !entryExpression(pass, argument, false) {
				return false
			}
		}

		return true
	}

	return false
}

func entryInteger(value types.Type) bool {
	basic, ok := value.Underlying().(*types.Basic)

	return ok && basic.Info()&types.IsInteger != 0
}
