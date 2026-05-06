package staticcheck

import (
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
)

// ThinWrapperAnalyzer flags single-statement wrapper functions whose
// only body is "return TARGET(args...)" where TARGET is a known
// constructor that golangci-lint's wrapcheck has been configured to
// ignore. The pattern hides a launderable call from wrapcheck without
// adding any logic, and was specifically observed when an agent
// invented a grpcError helper to bypass the wrapcheck finding on
// status.Errorf rather than wrap the error at the call site.
//
// Skipped for: _test.go, generated, protobuf, analyzer-self.
//
// To declare a legitimate wrapper that adds real logic, the function
// body must contain at least one statement that is NOT a forwarding
// return to one of the watched targets. A no-op log line, a default,
// a metric, or a precondition check all defeat the rule.
var ThinWrapperAnalyzer = &analysis.Analyzer{
	Name: "thin_wrapper_to_launderable_call",
	Doc:  "rejects single-statement wrappers around wrapcheck-ignored constructors (e.g. status.Error/Errorf); call the constructor directly so wrapcheck sees the real call site",
	Run:  runThinWrapper,
}

// thinWrapperWatchedTargets enumerates qualified call targets that
// wrapcheck's ignore-sigs allowlist exempts. A wrapper that only
// forwards to one of these is dead indirection.
var thinWrapperWatchedTargets = map[string]map[string]struct{}{
	"status": {
		"Error":  {},
		"Errorf": {},
		"New":    {},
		"Newf":   {},
	},
	"errors": {
		"New":  {},
		"Wrap": {},
	},
	"fmt": {
		"Errorf": {},
	},
}

func runThinWrapper(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file, path) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Body == nil || len(fn.Body.List) != 1 {
				continue
			}
			ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			call, ok := ret.Results[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			if !isWatchedLaunderableCall(call) {
				continue
			}
			if !forwardsAllParams(fn, call) {
				continue
			}
			reportAtf(pass, file, fn.Pos(), "function %q is a thin wrapper around a wrapcheck-ignored constructor; call it directly at the use site so wrapcheck sees the real call (or add real logic to the wrapper)", fn.Name.Name)
		}
	}
	return nil, nil
}

func isWatchedLaunderableCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	names, ok := thinWrapperWatchedTargets[pkgIdent.Name]
	if !ok {
		return false
	}
	_, ok = names[sel.Sel.Name]
	return ok
}

// forwardsAllParams returns true when every argument to the wrapped
// call is either a direct parameter ident from the enclosing function
// or a variadic spread of one. This is the signature of a pure
// pass-through wrapper. A wrapper that constructs literals, computes
// a value, or reorders args fails this check and is left alone.
func forwardsAllParams(fn *ast.FuncDecl, call *ast.CallExpr) bool {
	paramNames := collectParamNames(fn.Type.Params)
	if len(paramNames) == 0 {
		return false
	}
	if len(call.Args) != len(paramNames) {
		return false
	}
	for i, arg := range call.Args {
		ident, ok := unwrapVariadic(arg, call.Ellipsis, i == len(call.Args)-1)
		if !ok {
			return false
		}
		if ident.Name != paramNames[i] {
			return false
		}
	}
	return true
}

func collectParamNames(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var names []string
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			return nil
		}
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

func unwrapVariadic(arg ast.Expr, ellipsis token.Pos, isLast bool) (*ast.Ident, bool) {
	if isLast && ellipsis.IsValid() {
		ident, ok := arg.(*ast.Ident)
		return ident, ok
	}
	ident, ok := arg.(*ast.Ident)
	return ident, ok
}
