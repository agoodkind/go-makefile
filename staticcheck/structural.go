package staticcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// GoroutineWithoutRecoverAnalyzer flags `go func() {...}()` launches
// where the function body does not include a deferred recover().
// Long-lived daemons crash silently if a launched goroutine panics.
//
// Allowed escapes:
//   - test files
//   - //nolint:goroutine_without_recover on the `go` keyword line
//   - functions tiny enough that panic is structurally impossible
//     (we do NOT try to detect that; rely on nolint)
var GoroutineWithoutRecoverAnalyzer = &analysis.Analyzer{
	Name: "goroutine_without_recover",
	Doc:  "rejects launched goroutines that lack a deferred recover()",
	Run:  runGoroutineWithoutRecover,
}

func runGoroutineWithoutRecover(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			gostmt, ok := node.(*ast.GoStmt)
			if !ok {
				return true
			}
			if hasNolintComment(file, pass.Fset, gostmt.Pos(), "goroutine_without_recover") {
				return true
			}
			lit := goroutineFuncLit(gostmt.Call)
			if lit == nil {
				// Goroutine launches a named func. Caller's func body is not
				// visible here without a callgraph. Force the launch site to
				// wrap in a FuncLit with recover, or use an explicit nolint.
				pass.Reportf(gostmt.Pos(), "goroutine launched against a named func; wrap in a func literal that defers recover() or use //nolint:goroutine_without_recover")
				return true
			}
			if !funcLitHasDeferredRecover(lit) {
				pass.Reportf(gostmt.Pos(), "goroutine launched without a deferred recover(); add `defer func() { if r := recover(); r != nil { slog.Error(...) } }()` or //nolint:goroutine_without_recover")
			}
			return true
		})
	}
	return nil, nil
}

func goroutineFuncLit(call *ast.CallExpr) *ast.FuncLit {
	if call == nil {
		return nil
	}
	lit, ok := call.Fun.(*ast.FuncLit)
	if !ok {
		return nil
	}
	return lit
}

func funcLitHasDeferredRecover(lit *ast.FuncLit) bool {
	if lit == nil || lit.Body == nil {
		return false
	}
	found := false
	ast.Inspect(lit.Body, func(node ast.Node) bool {
		if found {
			return false
		}
		def, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(def, func(inner ast.Node) bool {
			if found {
				return false
			}
			c, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "recover" {
				found = true
				return false
			}
			return true
		})
		return true
	})
	return found
}

// SilentDeferCloseAnalyzer flags `defer x.Close()` patterns where
// the Close return value is dropped silently. Standard idioms:
//
//	defer func() { _ = f.Close() }()
//	defer func() { if err := f.Close(); err != nil { slog.Warn(...) } }()
//
// We accept either form. The explicit `_ =` form is treated as
// intentional silence. The slog form is the audit-trail variant.
// What we reject is `defer f.Close()` bare, which both ignores the
// error and signals no thought.
var SilentDeferCloseAnalyzer = &analysis.Analyzer{
	Name: "silent_defer_close",
	Doc:  "rejects `defer x.Close()` bare patterns; require explicit `_ =` or err handling",
	Run:  runSilentDeferClose,
}

func runSilentDeferClose(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			def, ok := node.(*ast.DeferStmt)
			if !ok {
				return true
			}
			if hasNolintComment(file, pass.Fset, def.Pos(), "silent_defer_close") {
				return true
			}
			_, name, ok := selectorName(def.Call.Fun)
			if !ok {
				return true
			}
			if !isCloseMethodName(name) {
				return true
			}
			pass.Reportf(def.Pos(), "bare `defer .%s()` drops the error; wrap in `defer func() { _ = ...%s() }()` for intentional silence or check err with slog.Warn", name, name)
			return true
		})
	}
	return nil, nil
}

func isCloseMethodName(name string) bool {
	switch name {
	case "Close", "CloseSend", "Shutdown", "Stop":
		return true
	}
	return false
}

// SlogMissingTraceIDAnalyzer flags slog.Info/Warn/Error/Debug calls
// inside functions that receive a [context.Context] but that do NOT
// reference the context anywhere in the call's keyvals. Purpose:
// surface log lines that are missing trace correlation.
//
// This is a heuristic and produces false positives. It is here to
// PROMPT the author, not to gate. Use //nolint:slog_missing_trace_id
// to dismiss, or switch to InfoContext / WarnContext / ErrorContext
// which threads the context-attached attrs automatically.
var SlogMissingTraceIDAnalyzer = &analysis.Analyzer{
	Name: "slog_missing_trace_id",
	Doc:  "warns when slog calls inside context-receiving funcs do not propagate context",
	Run:  runSlogMissingTraceID,
}

func runSlogMissingTraceID(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ctxName := contextParamName(fn)
			if ctxName == "" {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if !isAnyLevelSlogCall(call) {
					return true
				}
				if hasNolintComment(file, pass.Fset, call.Pos(), "slog_missing_trace_id") {
					return true
				}
				_, name, _ := selectorName(call.Fun)
				if strings.HasSuffix(name, "Context") {
					return true
				}
				if callReferencesIdent(call, ctxName) {
					return true
				}
				pass.Reportf(call.Pos(), "slog call inside func taking context.Context does not pass the ctx; use slog.%sContext or include ctx for trace correlation", name)
				return true
			})
		}
	}
	return nil, nil
}

func isAnyLevelSlogCall(call *ast.CallExpr) bool {
	receiver, name, ok := selectorName(call.Fun)
	if !ok {
		return false
	}
	if !isLikelyLoggerReceiver(receiver) {
		return false
	}
	switch name {
	case "Debug", "Info", "Warn", "Error",
		"DebugContext", "InfoContext", "WarnContext", "ErrorContext":
		return true
	}
	return false
}

func contextParamName(fn *ast.FuncDecl) string {
	if fn.Type == nil || fn.Type.Params == nil {
		return ""
	}
	for _, field := range fn.Type.Params.List {
		sel, ok := field.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			continue
		}
		if x.Name == "context" && sel.Sel.Name == "Context" {
			for _, n := range field.Names {
				return n.Name
			}
		}
	}
	return ""
}

func callReferencesIdent(node ast.Node, name string) bool {
	if name == "" {
		return false
	}
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}
