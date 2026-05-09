package staticcheck

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

// LifecycleSilentCloseErrAnalyzer flags Close(reason string) error
// implementations whose body discards the error from a downstream
// Close/Cancel/Signal/Stop call AND returns nil unconditionally. The
// empirical case is wsConnCloser.Close:
//
//	func (c *wsConnCloser) Close(reason string) error {
//	    _ = c.session.Conn.Close()
//	    return nil
//	}
//
// The drain reports clean even when the underlying close failed. The
// closer should at minimum wrap the error with context, or log it at
// debug, before returning nil.
//
// Configurable behaviour:
//
//	-closer_methods  comma-separated method names whose
//	                 (reason string) error signature triggers the
//	                 detector. Default is "Close".
//
// The diagnostic carries "[LIFECYCLE002]". Document intentional
// exceptions in the staticcheck-extra baseline, not via inline
// directives.
var LifecycleSilentCloseErrAnalyzer = newLifecycleSilentCloseErrAnalyzer()

func newLifecycleSilentCloseErrAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name: "lifecycle_silent_close_err",
		Doc:  "warns when a (reason string) error closer discards Close/Cancel/Signal errors and returns nil unconditionally",
		Run:  runLifecycleSilentCloseErr,
	}
	a.Flags.String("closer_methods", "", "comma-separated method names that should be treated as closers; default is Close")
	return a
}

func runLifecycleSilentCloseErr(pass *analysis.Pass) (any, error) {
	closerNames := csvFlagSet(&pass.Analyzer.Flags, "closer_methods")
	if len(closerNames) == 0 {
		closerNames = map[string]struct{}{"Close": {}}
	}
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil || fn.Name == nil {
				continue
			}
			if _, listed := closerNames[fn.Name.Name]; !listed {
				continue
			}
			if !isReasonStringCloseSignature(fn) {
				continue
			}
			if !returnsNilUnconditionally(fn.Body) {
				continue
			}
			reportSilentDiscards(pass, file, fn.Body)
		}
	}
	return nil, nil
}

func reportSilentDiscards(pass *analysis.Pass, file *ast.File, body *ast.BlockStmt) {
	ast.Inspect(body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || !isUnderscoreAssign(assign) {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if !callTargetIsCloseLikeMethod(call) {
			return true
		}
		_, name, _ := selectorName(call.Fun)
		reportAtf(pass, file, assign.Pos(),
			"[LIFECYCLE002] discarded error from .%s() inside a (reason string) error closer that returns nil; wrap with fmt.Errorf or log at debug, otherwise the closer reports clean even when the underlying call failed",
			name)
		return true
	})
}

func callTargetIsCloseLikeMethod(call *ast.CallExpr) bool {
	_, name, ok := selectorName(call.Fun)
	if !ok {
		return false
	}
	switch name {
	case "Close", "CloseSend", "Cancel", "Signal", "Stop", "Shutdown":
		return true
	}
	return false
}

// returnsNilUnconditionally inspects every return statement reachable
// in body and returns true only when each one returns either no value
// or the literal `nil`. A function that may return a non-nil error
// from any path fails the check.
func returnsNilUnconditionally(body *ast.BlockStmt) bool {
	allNil := true
	hasReturn := false
	ast.Inspect(body, func(node ast.Node) bool {
		ret, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		hasReturn = true
		if len(ret.Results) == 0 {
			return true
		}
		for _, r := range ret.Results {
			if !isNilLit(r) {
				allNil = false
				return false
			}
		}
		return true
	})
	return hasReturn && allNil
}
