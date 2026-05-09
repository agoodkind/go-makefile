package staticcheck

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// LifecycleNoopCloserAnalyzer flags Closer.Close(reason string) error
// implementations whose body does no real shutdown work. The
// empirical case is a `noopCloser` whose only method is:
//
//	func (noopCloser) Close(reason string) error { return nil }
//
// Registered against a livetrack lifecycle, the closer satisfies the
// contract while doing nothing on reload, so resources leak silently.
//
// The detector inspects every receiver method whose signature matches
// `Close(reason string) error` (parameter name irrelevant, types
// strict). A body is considered real-work when it contains any of:
//
//   - a call whose function expression has a type of
//     [context.CancelFunc], or a call named "cancel" / "stop"
//   - a Close() call on a value whose type is one of [net.Conn],
//     [*os.File], [*os.Process], [*tls.Conn], [*http.Server], or implements
//     [io.Closer] (not the closer itself, to avoid trivial recursion)
//   - a Signal() call on a value of type [*os.Process]
//   - a send on a channel
//   - a recv from a channel
//
// A body that contains only return-nil and slog calls is flagged as
// no-op. Receiver types listed in the `-allowlist` flag are skipped
// (qualified names: `goodkind.io/clyde/internal/mitm.mitmHTTPCloser`).
//
// The diagnostic carries "[LIFECYCLE001]". Document intentional
// exceptions in the staticcheck-extra baseline or via the -allowlist
// flag, not via inline directives.
var LifecycleNoopCloserAnalyzer = newLifecycleNoopCloserAnalyzer()

func newLifecycleNoopCloserAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name: "lifecycle_noop_closer",
		Doc:  "rejects Close(reason string) error bodies that do no real shutdown work",
		Run:  runLifecycleNoopCloser,
	}
	a.Flags.String("allowlist", "", "comma-separated qualified receiver-type names whose empty Close(reason string) error body is intentional")
	return a
}

func runLifecycleNoopCloser(pass *analysis.Pass) (any, error) {
	allowlist := csvFlagSet(&pass.Analyzer.Flags, "allowlist")
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			if !isReasonStringCloseSignature(fn) {
				continue
			}
			recvType := receiverQualifiedTypeName(pass, fn)
			if recvType != "" {
				if _, allow := allowlist[recvType]; allow {
					continue
				}
			}
			if bodyDoesRealShutdown(pass, fn.Body) {
				continue
			}
			reportAtf(pass, file, fn.Pos(),
				"[LIFECYCLE001] Close(reason string) error on %q has a no-op body; the lifecycle will treat the resource as released without doing anything. Call cancel(), Close(), or Signal() on the underlying resource, or add the receiver type to the allowlist if the no-op is intentional",
				displayReceiverName(fn, recvType))
		}
	}
	return nil, nil
}

func isReasonStringCloseSignature(fn *ast.FuncDecl) bool {
	if fn.Name == nil || fn.Name.Name != "Close" {
		return false
	}
	if fn.Type == nil {
		return false
	}
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	param := fn.Type.Params.List[0]
	if !exprIsBuiltinIdent(param.Type, "string") {
		return false
	}
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	return exprIsBuiltinIdent(fn.Type.Results.List[0].Type, "error")
}

func exprIsBuiltinIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == name
}

func receiverQualifiedTypeName(pass *analysis.Pass, fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	if pass.Pkg == nil {
		return ident.Name
	}
	return pass.Pkg.Path() + "." + ident.Name
}

func displayReceiverName(fn *ast.FuncDecl, qualified string) string {
	if qualified != "" {
		return qualified
	}
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "(unknown)"
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return "(unknown)"
}

// bodyDoesRealShutdown returns true when the function body contains a
// statement that genuinely affects external resources or
// synchronisation. The check is heuristic and biased toward
// false-negatives: we want to err on the side of NOT flagging real
// shutdowns. The empty body and the single-return-nil body are the
// targets.
func bodyDoesRealShutdown(pass *analysis.Pass, body *ast.BlockStmt) bool {
	hasRealShutdown := false
	ast.Inspect(body, func(node ast.Node) bool {
		if hasRealShutdown {
			return false
		}
		switch n := node.(type) {
		case *ast.SendStmt:
			hasRealShutdown = true
			return false
		case *ast.UnaryExpr:
			if n.Op.String() == "<-" {
				hasRealShutdown = true
				return false
			}
		case *ast.CallExpr:
			if callIsRealShutdown(pass, n) {
				hasRealShutdown = true
				return false
			}
		}
		return true
	})
	return hasRealShutdown
}

func callIsRealShutdown(pass *analysis.Pass, call *ast.CallExpr) bool {
	if isLoggingCall(call) {
		return false
	}
	if callIsNamedCancelOrStop(call) {
		return true
	}
	if callTargetIsContextCancelFunc(pass, call) {
		return true
	}
	if callIsCloserMethod(pass, call) {
		return true
	}
	return callIsProcessSignal(pass, call)
}

func isLoggingCall(call *ast.CallExpr) bool {
	receiver, name, ok := selectorName(call.Fun)
	if !ok {
		return false
	}
	if !isLikelyLoggerReceiver(receiver) && receiver != "slog" {
		return false
	}
	switch name {
	case "Debug", "Info", "Warn", "Error",
		"DebugContext", "InfoContext", "WarnContext", "ErrorContext",
		"Log", "LogAttrs", "Print", "Printf", "Println":
		return true
	}
	return false
}

func callIsNamedCancelOrStop(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return isCancelLikeName(fun.Name)
	case *ast.SelectorExpr:
		return isCancelLikeName(fun.Sel.Name)
	}
	return false
}

func isCancelLikeName(name string) bool {
	switch strings.ToLower(name) {
	case "cancel", "stop", "shutdown", "gracefulstop", "kill", "wait":
		return true
	}
	return false
}

func callTargetIsContextCancelFunc(pass *analysis.Pass, call *ast.CallExpr) bool {
	if pass.TypesInfo == nil {
		return false
	}
	t := pass.TypesInfo.TypeOf(call.Fun)
	if t == nil {
		return false
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "context" && obj.Name() == "CancelFunc"
}

func callIsCloserMethod(pass *analysis.Pass, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Close" {
		return false
	}
	if pass.TypesInfo == nil {
		return false
	}
	recv := pass.TypesInfo.TypeOf(sel.X)
	if recv == nil {
		return false
	}
	if isOneOfNamed(recv, []namedRef{
		{"net", "Conn"},
		{"os", "File"},
		{"os", "Process"},
		{"crypto/tls", "Conn"},
		{"net/http", "Server"},
		{"io", "ReadCloser"},
		{"io", "WriteCloser"},
		{"io", "Closer"},
	}) {
		return true
	}
	return implementsIOCloser(recv)
}

type namedRef struct {
	pkgPath string
	name    string
}

func underlyingNamed(t types.Type) (*types.Named, bool) {
	switch v := t.(type) {
	case *types.Named:
		return v, true
	case *types.Pointer:
		if named, ok := v.Elem().(*types.Named); ok {
			return named, true
		}
	}
	return nil, false
}

func isOneOfNamed(t types.Type, refs []namedRef) bool {
	named, ok := underlyingNamed(t)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	for _, r := range refs {
		if obj.Pkg().Path() == r.pkgPath && obj.Name() == r.name {
			return true
		}
	}
	return false
}

func implementsIOCloser(t types.Type) bool {
	mset := types.NewMethodSet(types.NewPointer(t))
	for selection := range mset.Methods() {
		method := selection.Obj()
		if method == nil {
			continue
		}
		fn, ok := method.(*types.Func)
		if !ok {
			continue
		}
		if fn.Name() != "Close" {
			continue
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			continue
		}
		if sig.Params().Len() != 0 {
			continue
		}
		if sig.Results().Len() != 1 {
			continue
		}
		if isErrorType(sig.Results().At(0).Type()) {
			return true
		}
	}
	return false
}

func callIsProcessSignal(pass *analysis.Pass, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Signal" {
		return false
	}
	if pass.TypesInfo == nil {
		return false
	}
	recv := pass.TypesInfo.TypeOf(sel.X)
	if recv == nil {
		return false
	}
	return isOneOfNamed(recv, []namedRef{{"os", "Process"}})
}
