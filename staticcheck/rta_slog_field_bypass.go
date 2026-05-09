package staticcheck

import (
	"go/ast"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// RTASlogFieldBypassAnalyzer flags a deadcode RTA bypass shape where a
// structured slog call boxes a marker-typed value into a key-value
// attribute that no real consumer reads. The empirical case
// (CLYDE-308 phase 2) is:
//
//	// Without this, deadcode does not see SupervisorMeta as live because
//	// Registry[T] is generic and RTA loses the type parameter binding.
//	slog.InfoContext(ctx, "supervisor.starting", "supervisor_meta", supervisorMeta)
//
// Removing the field has no observable telemetry effect; the only
// reason it exists is to drag SupervisorMeta.IsLivetrackMeta into the
// SSA call graph so the deadcode analyzer treats it as live. This is
// the slog-field sibling of [RTA002] (synthetic marker call) and
// [RTA003] (throwaway gRPC registration).
//
// The detector keys on three signals that all must hold for a
// finding:
//
//  1. The call is a structured slog event: slog.{Debug,Info,Warn,Error}
//     or their *Context variants, called either as a package function
//     or as a method on a logger value (any receiver whose name
//     contains "log", matching the convention used by sibling slog
//     analyzers in this set).
//  2. One of the variadic key-value pairs has a value whose static
//     type is a struct (concrete, not interface) carrying a method
//     matching the marker pattern: no arguments, no return values,
//     empty body. The set of accepted marker method names is
//     configurable via the -marker_methods flag and defaults to
//     "IsLivetrackMeta".
//  3. The boxed value is constructed inline at the call site, OR is
//     a local variable that is referenced only from this slog call.
//     A value flowing into any non-slog consumer (assigned to a
//     struct field, passed to a non-slog function, returned, sent on
//     a channel, etc.) is treated as load-bearing and skipped.
//
// Narrowing notes (deliberate false-negative admissions):
//
//   - The detector does not chase values across function boundaries
//     or through helper constructors that inline-build the marker. A
//     constructor returning a marker struct, called inline, will not
//     fire unless the constructor is itself trivially recognised as
//     a composite-literal wrapper. This avoids false positives on
//     legitimate structured logs where the value happens to be a
//     marker-bearing type used as real telemetry.
//   - The detector does not fire on calls to slog.LogAttrs or on
//     slog.With. The bypass shape we have observed in clyde adopters
//     uses positional key-value pairs.
//   - The detector requires the marker method to have an empty body
//     (no statements). A marker method with even a stray return
//     statement is treated as "real" and the type is not flagged.
//
// The diagnostic carries the literal "[RTA004]". Document
// intentional exceptions in the staticcheck-extra baseline rather
// than via inline directives.
var RTASlogFieldBypassAnalyzer = newRTASlogFieldBypassAnalyzer()

func newRTASlogFieldBypassAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name: "rta_slog_field_bypass",
		Doc:  "rejects slog calls whose value-position attribute boxes a marker-typed struct only to keep its method reachable for deadcode",
		Run:  runRTASlogFieldBypass,
	}
	a.Flags.String("marker_methods", defaultRTASlogMarkerMethodList,
		"comma-separated method names that identify a deadcode marker type when present as an empty no-arg no-return method")
	return a
}

const defaultRTASlogMarkerMethodList = "IsLivetrackMeta"

var rtaSlogTrackedFuncs = map[string]struct{}{
	"Debug":        {},
	"DebugContext": {},
	"Info":         {},
	"InfoContext":  {},
	"Warn":         {},
	"WarnContext":  {},
	"Error":        {},
	"ErrorContext": {},
}

func runRTASlogFieldBypass(pass *analysis.Pass) (any, error) {
	markers := csvFlagSet(&pass.Analyzer.Flags, "marker_methods")
	if len(markers) == 0 {
		markers = map[string]struct{}{"IsLivetrackMeta": {}}
	}
	emptyMethods := collectEmptyMarkerMethods(pass.Files, markers)
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		checkRTASlogFieldBypassInFile(pass, file, markers, emptyMethods)
	}
	return nil, nil
}

// collectEmptyMarkerMethods scans every package file once for
// receiver method declarations whose name matches the marker set
// and whose body is syntactically empty. The result keyed on
// (recvType, methodName) is used downstream so we only flag values
// whose marker method we have positively verified to be empty in
// the package's source. Methods declared in external packages are
// absent from this map and intentionally produce no findings, which
// is the documented narrowing in the analyzer doc.
type rtaSlogMarkerKey struct {
	recvTypeName string
	methodName   string
}

func collectEmptyMarkerMethods(files []*ast.File, markers map[string]struct{}) map[rtaSlogMarkerKey]struct{} {
	out := map[rtaSlogMarkerKey]struct{}{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Recv == nil || fn.Name == nil {
				continue
			}
			if _, accepted := markers[fn.Name.Name]; !accepted {
				continue
			}
			if !funcDeclIsNoArgsNoReturns(fn) {
				continue
			}
			if !funcDeclBodyIsEmpty(fn) {
				continue
			}
			recv := receiverTypeNameFromFuncDecl(fn)
			if recv == "" {
				continue
			}
			out[rtaSlogMarkerKey{recvTypeName: recv, methodName: fn.Name.Name}] = struct{}{}
		}
	}
	return out
}

func funcDeclIsNoArgsNoReturns(fn *ast.FuncDecl) bool {
	if fn.Type == nil {
		return false
	}
	if fn.Type.Params != nil && len(fn.Type.Params.List) > 0 {
		return false
	}
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		return false
	}
	return true
}

func funcDeclBodyIsEmpty(fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	return len(fn.Body.List) == 0
}

// receiverTypeNameFromFuncDecl returns the unqualified name of a
// method's receiver type, looking through a single pointer
// indirection. Generic receivers (with type parameter brackets) are
// stripped down to the base name. Returns the empty string for
// shapes the analyzer does not handle.
func receiverTypeNameFromFuncDecl(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if idx, ok := expr.(*ast.IndexExpr); ok {
		expr = idx.X
	}
	if idx, ok := expr.(*ast.IndexListExpr); ok {
		expr = idx.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

func checkRTASlogFieldBypassInFile(pass *analysis.Pass, file *ast.File, markers map[string]struct{}, emptyMethods map[rtaSlogMarkerKey]struct{}) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		checkRTASlogFieldBypassInFunc(pass, file, fn, markers, emptyMethods)
	}
}

func checkRTASlogFieldBypassInFunc(pass *analysis.Pass, file *ast.File, fn *ast.FuncDecl, markers map[string]struct{}, emptyMethods map[rtaSlogMarkerKey]struct{}) {
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isTrackedSlogCall(call) {
			return true
		}
		valueArgs := slogValueArgPositions(call)
		for _, idx := range valueArgs {
			arg := call.Args[idx]
			markerName, recvType, isMarker := classifyMarkerValue(pass, arg, markers, emptyMethods)
			if !isMarker {
				continue
			}
			if !isInlineOrLocallyScopedValue(fn.Body, arg) {
				continue
			}
			fieldKey := slogFieldKeyForValue(call, idx)
			reportAtf(pass, file, arg.Pos(),
				"[RTA004] slog field %q boxes a marker-typed value of %q (carries empty method %q) and the value has no non-slog consumer; this is a deadcode RTA bypass. Drop the field, or route the value through a real consumer if it is genuinely load-bearing",
				fieldKey, formatType(recvType), markerName)
		}
		return true
	})
}

// isTrackedSlogCall returns true when the call is one of the
// slog.{Debug,Info,Warn,Error}[Context] functions, called either as
// `slog.Info(...)` or as a method on a logger-named receiver such as
// `log.InfoContext(...)`. The receiver heuristic mirrors
// isLikelyLoggerReceiver used by the sibling slog analyzers.
func isTrackedSlogCall(call *ast.CallExpr) bool {
	receiver, name, ok := selectorName(call.Fun)
	if !ok {
		return false
	}
	if _, tracked := rtaSlogTrackedFuncs[name]; !tracked {
		return false
	}
	if receiver == "slog" {
		return true
	}
	return isLikelyLoggerReceiver(receiver)
}

// slogValueArgPositions returns the indices of the value-position
// arguments inside a tracked slog call. The first argument is either
// a context (for *Context variants) or the message string. Subsequent
// args are alternating key-value pairs; values sit at every other
// index after the message argument.
func slogValueArgPositions(call *ast.CallExpr) []int {
	_, name, ok := selectorName(call.Fun)
	if !ok {
		return nil
	}
	messageIndex := 0
	if strings.HasSuffix(name, "Context") {
		messageIndex = 1
	}
	if len(call.Args) <= messageIndex+1 {
		return nil
	}
	var out []int
	for i := messageIndex + 2; i < len(call.Args); i += 2 {
		out = append(out, i)
	}
	return out
}

// slogFieldKeyForValue returns the literal string key paired with the
// value at index valueIdx. When the key is not a string literal it
// falls back to a positional descriptor so the diagnostic still
// pinpoints the offending pair.
func slogFieldKeyForValue(call *ast.CallExpr, valueIdx int) string {
	if valueIdx == 0 {
		return "(unknown key)"
	}
	keyExpr := call.Args[valueIdx-1]
	if value, ok := stringLiteral(keyExpr); ok {
		return value
	}
	return "(non-literal key)"
}

// classifyMarkerValue inspects an expression and returns the marker
// method name and the underlying named struct type when the
// expression's static type carries a marker method whose body the
// per-package syntax scan in collectEmptyMarkerMethods has confirmed
// to be empty. Returns isMarker=false otherwise. Pointer wrappers
// around struct types are looked through so `&Meta{}` is treated
// the same as `Meta{}`.
func classifyMarkerValue(pass *analysis.Pass, expr ast.Expr, markers map[string]struct{}, emptyMethods map[rtaSlogMarkerKey]struct{}) (markerName string, recvType types.Type, isMarker bool) {
	if pass.TypesInfo == nil {
		return "", nil, false
	}
	staticType := pass.TypesInfo.TypeOf(expr)
	if staticType == nil {
		return "", nil, false
	}
	named, structType := unwrapToStruct(staticType)
	if structType == nil || named == nil {
		return "", nil, false
	}
	for method := range named.Methods() {
		if _, accepted := markers[method.Name()]; !accepted {
			continue
		}
		if !signatureIsNoArgsNoReturns(method.Type()) {
			continue
		}
		key := rtaSlogMarkerKey{recvTypeName: named.Obj().Name(), methodName: method.Name()}
		if _, ok := emptyMethods[key]; !ok {
			continue
		}
		return method.Name(), staticType, true
	}
	return "", nil, false
}

// unwrapToStruct strips a single pointer indirection and returns the
// named type and its underlying struct definition when present. The
// detector intentionally rejects unnamed struct literals because the
// CLYDE-308 bypass shape always carries a named type whose method
// set is what RTA needs to mark live.
func unwrapToStruct(t types.Type) (*types.Named, *types.Struct) {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil, nil
	}
	structType, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, nil
	}
	return named, structType
}

func signatureIsNoArgsNoReturns(t types.Type) bool {
	sig, ok := t.(*types.Signature)
	if !ok {
		return false
	}
	return sig.Params().Len() == 0 && sig.Results().Len() == 0
}

// isInlineOrLocallyScopedValue applies the false-positive guard from
// criterion 3 of the analyzer doc. The value is acceptable for
// flagging when one of the following holds:
//
//   - It is a composite literal or an address-of composite literal
//     constructed at the call site.
//   - It is an identifier referring to a local variable whose only
//     uses inside fnBody are the slog call itself and (optionally)
//     the assignment that constructed it inline. Any other usage is
//     treated as a real consumer and the value is skipped.
func isInlineOrLocallyScopedValue(fnBody *ast.BlockStmt, expr ast.Expr) bool {
	if isCompositeOrAddrOfComposite(expr) {
		return true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	if ident.Name == "_" {
		return false
	}
	return localVarHasNoOtherConsumer(fnBody, ident)
}

func isCompositeOrAddrOfComposite(expr ast.Expr) bool {
	if _, ok := expr.(*ast.CompositeLit); ok {
		return true
	}
	unary, ok := expr.(*ast.UnaryExpr)
	if !ok {
		return false
	}
	if _, ok := unary.X.(*ast.CompositeLit); ok {
		return true
	}
	return false
}

// localVarHasNoOtherConsumer walks fnBody and returns true when
// every reference to a same-name identifier appears either inside a
// tracked slog call's argument list or as the LHS of the
// declaration that constructs it from a composite literal. Any
// other reference (passed to another function, returned, assigned
// to a struct field, etc.) is a real consumer and disqualifies the
// finding.
func localVarHasNoOtherConsumer(fnBody *ast.BlockStmt, target *ast.Ident) bool {
	parents := buildParentMap(fnBody)
	otherConsumer := false
	ast.Inspect(fnBody, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != target.Name {
			return true
		}
		if isDeclarationFromCompositeLit(ident, parents) {
			return true
		}
		if isInsideTrackedSlogCallArgs(ident, parents) {
			return true
		}
		if isOwnIdentInDecl(ident, parents) {
			return true
		}
		otherConsumer = true
		return true
	})
	return !otherConsumer
}

// isDeclarationFromCompositeLit returns true when ident is the LHS
// of a `:=` assignment whose RHS is a composite literal or an
// address-of composite literal. This is the recognised inline
// construction shape; references that match this pattern are not
// treated as real consumers.
func isDeclarationFromCompositeLit(ident *ast.Ident, parents map[ast.Node]ast.Node) bool {
	parent := parents[ident]
	assign, ok := parent.(*ast.AssignStmt)
	if !ok {
		return false
	}
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return false
	}
	if assign.Lhs[0] != ident {
		return false
	}
	return isCompositeOrAddrOfComposite(assign.Rhs[0])
}

// isOwnIdentInDecl returns true when ident is the LHS name of a
// var or short-var declaration (the binding occurrence), regardless
// of the RHS shape. The binding occurrence is not a consumer; the
// consumer test is about uses, not the declaration itself.
func isOwnIdentInDecl(ident *ast.Ident, parents map[ast.Node]ast.Node) bool {
	parent := parents[ident]
	switch p := parent.(type) {
	case *ast.AssignStmt:
		return slices.Contains(p.Lhs, ast.Expr(ident))
	case *ast.ValueSpec:
		return slices.Contains(p.Names, ident)
	}
	return false
}

// isInsideTrackedSlogCallArgs walks up from ident and returns true
// when ident appears inside the argument list of a tracked slog
// call. Any ident-bearing expression nested inside such a call
// (selector receiver, index expression, etc.) counts: the value is
// flowing into the slog field, not into a separate consumer.
func isInsideTrackedSlogCallArgs(ident *ast.Ident, parents map[ast.Node]ast.Node) bool {
	var node ast.Node = ident
	for {
		parent, ok := parents[node]
		if !ok || parent == nil {
			return false
		}
		if call, isCall := parent.(*ast.CallExpr); isCall {
			if call.Fun == node {
				return false
			}
			if isTrackedSlogCall(call) {
				return true
			}
			return false
		}
		node = parent
	}
}
