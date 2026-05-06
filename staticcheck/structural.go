package staticcheck

import (
	"go/ast"
	"go/token"
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
		if isTestFile(path) || isGeneratedFile(file, path) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
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

// funcLitHasDeferredRecover reports whether the function literal contains
// a deferred recover() that genuinely handles the panic. A recover() that
// is silently discarded or that immediately re-panics is treated as a
// stub and does NOT satisfy the analyzer; those are common laundering
// patterns where an LLM adds defer-recover boilerplate without actually
// rescuing the goroutine.
//
// Stub patterns rejected:
//
//	defer recover()                                // call value discarded
//	defer func() { recover() }()                   // discarded
//	defer func() { _ = recover() }()               // explicit discard
//	defer func() { if r := recover(); r != nil { panic(r) } }()  // re-panic
//
// Accepted patterns:
//
//	defer func() {
//	    if r := recover(); r != nil {
//	        slog.Error("recovered", "panic", r)
//	    }
//	}()
//
//	defer func() {
//	    if r := recover(); r != nil {
//	        errCh <- fmt.Errorf("panic: %v", r)
//	    }
//	}()
//
// In short: `recover()` must be bound to a name AND that name must be
// used somewhere that is not `panic(name)`.
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
		if deferIsRealRecoverHandler(def) {
			found = true
			return false
		}
		return true
	})
	return found
}

// deferIsRealRecoverHandler returns true if the defer expression
// contains a recover() call whose result is bound to a name AND that
// name is used in something other than panic(). Direct `defer recover()`
// is always rejected because the value cannot be inspected.
func deferIsRealRecoverHandler(def *ast.DeferStmt) bool {
	if def == nil || def.Call == nil {
		return false
	}
	// Direct `defer recover()` cannot do anything useful with the value.
	if id, ok := def.Call.Fun.(*ast.Ident); ok && id.Name == "recover" {
		return false
	}
	lit, ok := def.Call.Fun.(*ast.FuncLit)
	if !ok || lit.Body == nil {
		return false
	}
	return funcLitBodyHandlesRecover(lit.Body)
}

// funcLitBodyHandlesRecover walks a defer-FuncLit body and returns true
// only if recover() is bound to an identifier AND that identifier flows
// somewhere that constitutes real handling. Real handling means the
// recovered value is logged, sent to an error channel, stored in a
// shared variable, or otherwise observable. The body must NOT immediately
// re-panic with the recovered value (that is a stub: it provides the
// shape of recovery but does not rescue the goroutine).
func funcLitBodyHandlesRecover(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	recoverName := recoverBindingName(body)
	if recoverName == "" {
		return false
	}
	if bodyRepanicsWithName(body, recoverName) {
		return false
	}
	return recoverNameHasUsefulReference(body, recoverName)
}

func recoverBindingName(body *ast.BlockStmt) string {
	recoverName := ""
	ast.Inspect(body, func(n ast.Node) bool {
		if recoverName != "" {
			return false
		}
		name, ok := recoverBindingNameFromNode(n)
		if ok {
			recoverName = name
			return false
		}
		return true
	})
	return recoverName
}

func recoverBindingNameFromNode(node ast.Node) (string, bool) {
	switch s := node.(type) {
	case *ast.AssignStmt:
		return assignBindsRecover(s)
	case *ast.IfStmt:
		if init, ok := s.Init.(*ast.AssignStmt); ok {
			return assignBindsRecover(init)
		}
	}
	return "", false
}

func bodyRepanicsWithName(body *ast.BlockStmt, recoverName string) bool {
	rePanic := false
	ast.Inspect(body, func(n ast.Node) bool {
		if rePanic {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if callPanicsWithName(call, recoverName) {
			rePanic = true
			return false
		}
		return true
	})
	return rePanic
}

func callPanicsWithName(call *ast.CallExpr, recoverName string) bool {
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Name != "panic" || len(call.Args) != 1 {
		return false
	}
	argIdent, ok := call.Args[0].(*ast.Ident)
	return ok && argIdent.Name == recoverName
}

func recoverNameHasUsefulReference(body *ast.BlockStmt, recoverName string) bool {
	useful := false
	ast.Inspect(body, func(n ast.Node) bool {
		if useful {
			return false
		}
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name != recoverName {
			return true
		}
		if !identIsOnlyNilCheckOrPanicArg(body, ident) {
			useful = true
			return false
		}
		return true
	})
	return useful
}

func assignBindsRecover(s *ast.AssignStmt) (string, bool) {
	if s == nil || len(s.Lhs) == 0 || len(s.Rhs) == 0 {
		return "", false
	}
	call, ok := s.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Name != "recover" {
		return "", false
	}
	lhsIdent, ok := s.Lhs[0].(*ast.Ident)
	if !ok || lhsIdent.Name == "_" {
		return "", false
	}
	return lhsIdent.Name, true
}

// identIsOnlyNilCheckOrPanicArg returns true if the given Ident is in
// a context that does not constitute real handling: either as the sole
// argument to panic(), or as one side of a `name != nil` / `name == nil`
// comparison. In the bad3 case (`if r := recover(); r != nil { panic(r) }`)
// every reference to `r` falls into one of these contexts, so the
// deferred function does nothing with the recovered value.
func identIsOnlyNilCheckOrPanicArg(body *ast.BlockStmt, target *ast.Ident) bool {
	parents := buildParentMap(body)
	parent := parents[target]
	if call, ok := parent.(*ast.CallExpr); ok {
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
			return len(call.Args) == 1 && call.Args[0] == target
		}
	}
	if bin, ok := parent.(*ast.BinaryExpr); ok {
		if bin.Op == token.EQL || bin.Op == token.NEQ {
			if isNilLit(bin.X) || isNilLit(bin.Y) {
				return true
			}
		}
	}
	return false
}

func buildParentMap(root ast.Node) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	return parents
}

func isNilLit(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
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
		if isTestFile(path) || isGeneratedFile(file, path) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
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
		if isTestFile(path) || isGeneratedFile(file, path) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		checkSlogMissingTraceIDInFile(pass, file)
	}
	return nil, nil
}

func checkSlogMissingTraceIDInFile(pass *analysis.Pass, file *ast.File) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ctxName := contextParamName(fn)
		if ctxName == "" {
			continue
		}
		reportSlogCallsWithoutTraceID(pass, file, fn, ctxName)
	}
}

func reportSlogCallsWithoutTraceID(pass *analysis.Pass, file *ast.File, fn *ast.FuncDecl, ctxName string) {
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if slogCallHasTraceContext(pass, file, call, ctxName) {
			return true
		}
		_, name, _ := selectorName(call.Fun)
		pass.Reportf(call.Pos(), "slog call inside func taking context.Context does not pass the ctx; use slog.%sContext or include ctx for trace correlation", name)
		return true
	})
}

func slogCallHasTraceContext(pass *analysis.Pass, file *ast.File, call *ast.CallExpr, ctxName string) bool {
	if !isAnyLevelSlogCall(call) {
		return true
	}
	if hasNolintComment(file, pass.Fset, call.Pos(), "slog_missing_trace_id") {
		return true
	}
	_, name, _ := selectorName(call.Fun)
	return strings.HasSuffix(name, "Context") || callReferencesIdent(call, ctxName)
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
