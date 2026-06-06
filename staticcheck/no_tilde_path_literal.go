package staticcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// NoTildePathLiteralAnalyzer flags string literals that equal "~" or start
// with "~/" only when they reach a filesystem/path API. The Go standard
// library never expands tilde in path arguments ([os.Open], [os.MkdirAll],
// filepath.*, [exec.Command], and so on all treat "~" as a literal directory
// name), so such a literal in a path argument is a latent bug that creates a
// literal "~" directory at runtime instead of resolving to the user's home
// directory. Use [os.UserHomeDir] and [filepath.Join] to build the path
// explicitly.
//
// A tilde literal fires only when it reaches a path API in one of two ways:
//   - directly as an argument to an os.*, filepath.*, exec.*, or ioutil.* call,
//   - through a local variable that is assigned the literal and then passed to
//     one of those calls within the same function body.
//
// A tilde literal that is only returned, logged, or otherwise used for display
// is not flagged, since the standard library never sees it as a path. This is
// the rule's documented threat model: literals passed to path APIs.
var NoTildePathLiteralAnalyzer = &analysis.Analyzer{
	Name: "no_tilde_path_literal",
	Doc:  "rejects ~ / ~/ string literals that reach a path API; use os.UserHomeDir() + filepath.Join instead",
	Run:  runNoTildePathLiteral,
}

func runNoTildePathLiteral(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		reportTildePathLiterals(pass, file)
	}
	return nil, nil
}

// reportTildePathLiterals marks every tilde literal that reaches a path API and
// reports them in source order so the diagnostics are deterministic.
func reportTildePathLiterals(pass *analysis.Pass, file *ast.File) {
	flagged := map[*ast.BasicLit]bool{}
	markDirectPathArgTildes(file, flagged)
	markLocalAssignedPathTildes(file, flagged)
	if len(flagged) == 0 {
		return
	}
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || !flagged[lit] {
			return true
		}
		value, _ := stringLiteral(lit)
		reportAtf(pass, file, lit.Pos(), "hardcoded home directory %q; use os.UserHomeDir() or filepath.Join(homeDir, ...) since Go does not expand ~", value)
		return true
	})
}

// markDirectPathArgTildes marks tilde literals that appear inside the argument
// list of a path API call, including literals nested in composite argument
// expressions such as string concatenation.
func markDirectPathArgTildes(file *ast.File, flagged map[*ast.BasicLit]bool) {
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isPathAPICall(call) {
			return true
		}
		for _, arg := range call.Args {
			markTildeLiteralsIn(arg, flagged)
		}
		return true
	})
}

// markLocalAssignedPathTildes marks a tilde literal that is assigned to a local
// identifier and then passed, within the same function body, to a path API.
func markLocalAssignedPathTildes(file *ast.File, flagged map[*ast.BasicLit]bool) {
	ast.Inspect(file, func(node ast.Node) bool {
		var body *ast.BlockStmt
		switch fn := node.(type) {
		case *ast.FuncDecl:
			body = fn.Body
		case *ast.FuncLit:
			body = fn.Body
		}
		if body == nil {
			return true
		}
		markLocalTildesInBody(body, flagged)
		return true
	})
}

// markLocalTildesInBody resolves tilde-bound local identifiers in body and marks
// the bound literal when that identifier reaches a path API call.
func markLocalTildesInBody(body *ast.BlockStmt, flagged map[*ast.BasicLit]bool) {
	bindings := tildeLocalBindings(body)
	if len(bindings) == 0 {
		return
	}
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isPathAPICall(call) {
			return true
		}
		for _, arg := range call.Args {
			for _, name := range identNamesIn(arg) {
				if lit, ok := bindings[name]; ok {
					flagged[lit] = true
				}
			}
		}
		return true
	})
}

// tildeLocalBindings maps each local identifier bound to a tilde literal (via
// `:=`, `=`, or `var x = "~"`) to that literal, scanning the whole body.
func tildeLocalBindings(body *ast.BlockStmt) map[string]*ast.BasicLit {
	bindings := map[string]*ast.BasicLit{}
	ast.Inspect(body, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.AssignStmt:
			collectTildeAssign(stmt, bindings)
		case *ast.ValueSpec:
			collectTildeValueSpec(stmt, bindings)
		}
		return true
	})
	return bindings
}

func collectTildeAssign(stmt *ast.AssignStmt, bindings map[string]*ast.BasicLit) {
	for index, lhs := range stmt.Lhs {
		if index >= len(stmt.Rhs) {
			return
		}
		ident, ok := lhs.(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		if lit, ok := tildeStringLiteral(stmt.Rhs[index]); ok {
			bindings[ident.Name] = lit
		}
	}
}

func collectTildeValueSpec(spec *ast.ValueSpec, bindings map[string]*ast.BasicLit) {
	for index, name := range spec.Names {
		if index >= len(spec.Values) || name.Name == "_" {
			continue
		}
		if lit, ok := tildeStringLiteral(spec.Values[index]); ok {
			bindings[name.Name] = lit
		}
	}
}

// markTildeLiteralsIn marks every tilde literal found anywhere inside expr.
func markTildeLiteralsIn(expr ast.Expr, flagged map[*ast.BasicLit]bool) {
	ast.Inspect(expr, func(node ast.Node) bool {
		if lit, ok := node.(*ast.BasicLit); ok {
			if _, isTilde := tildeStringLiteral(lit); isTilde {
				flagged[lit] = true
			}
		}
		return true
	})
}

// tildeStringLiteral returns the BasicLit when expr is a string literal whose
// value equals "~" or starts with "~/".
func tildeStringLiteral(expr ast.Expr) (*ast.BasicLit, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok {
		return nil, false
	}
	value, ok := stringLiteral(lit)
	if !ok || !looksLikeTildeHomePath(value) {
		return nil, false
	}
	return lit, true
}

// identNamesIn returns the names of every identifier in expr.
func identNamesIn(expr ast.Expr) []string {
	var names []string
	ast.Inspect(expr, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok {
			names = append(names, ident.Name)
		}
		return true
	})
	return names
}

// isPathAPICall reports whether call targets a filesystem/path function in the
// os, filepath, exec, or ioutil packages, matched by the package selector.
func isPathAPICall(call *ast.CallExpr) bool {
	receiver, _, ok := selectorName(call.Fun)
	if !ok {
		return false
	}
	switch receiver {
	case "os", "filepath", "exec", "ioutil":
		return true
	}
	return false
}

func looksLikeTildeHomePath(value string) bool {
	if value == "~" {
		return true
	}
	return strings.HasPrefix(value, "~/")
}
