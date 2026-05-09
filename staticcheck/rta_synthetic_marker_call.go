package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// RTASyntheticMarkerCallAnalyzer flags a deadcode RTA bypass shape
// where a marker method is called once and the result is discarded so
// that the deadcode analyzer treats the method (and the implementing
// type's method set) as reachable. The empirical case looks like:
//
//	_ = SupervisorMeta{}.IsLivetrackMeta()
//	_ = meta.IsLivetrackMeta()
//	func init() { _ = WebMeta{}.IsLivetrackMeta() }
//
// The discriminator is intent: the call's only purpose is to count as
// a use. Real call sites consume the return value or pass it onward.
//
// Two configurable lists drive detection:
//
//	-marker_interfaces  comma-separated qualified interface names
//	                    whose methods, when called and discarded, are
//	                    suspicious. Example:
//	                    "goodkind.io/clyde/internal/livetrack.Meta".
//	-marker_methods     comma-separated method names. A discarded call
//	                    to a method whose name appears here is flagged
//	                    independently of interface implementation,
//	                    which lets projects that lack types-info
//	                    package paths still gate on names.
//
// When both lists are empty the analyzer no-ops and prints a single
// stderr warning so consumers know it ran unconfigured.
//
// A bonus high-confidence path fires regardless of configuration: any
// discarded method call within five lines of a comment matching
// (?i)deadcode|RTA|reachability is treated as a self-confessed bypass
// and reported with a stronger message.
//
// The diagnostic carries the literal "[RTA002]". Document intentional
// exceptions in the staticcheck-extra baseline, not via inline
// directives.
var RTASyntheticMarkerCallAnalyzer = newRTASyntheticMarkerCallAnalyzer()

func newRTASyntheticMarkerCallAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name: "rta_synthetic_marker_call",
		Doc:  "rejects discarded calls to project marker methods; these are deadcode RTA bypass alibis",
		Run:  runRTASyntheticMarkerCall,
	}
	a.Flags.String("marker_interfaces", "", "comma-separated qualified interface names (e.g. goodkind.io/clyde/internal/livetrack.Meta)")
	a.Flags.String("marker_methods", "", "comma-separated marker method names (e.g. IsLivetrackMeta)")
	return a
}

var rtaConfessionalCommentPattern = regexp.MustCompile(`(?i)deadcode|\brta\b|reachability`)

func runRTASyntheticMarkerCall(pass *analysis.Pass) (any, error) {
	cfg := loadRTASyntheticMarkerConfig(pass.Analyzer)
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		commentLines := collectConfessionalCommentLines(pass.Fset, file)
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := discardedMethodCall(node)
			if !ok {
				return true
			}
			if _, isExprStmt := node.(*ast.ExprStmt); isExprStmt && !callHasResultValue(pass, call) {
				return true
			}
			methodName, recvType, ok := resolveCalledMethod(pass, call)
			if !ok {
				return true
			}
			callLine := pass.Fset.Position(call.Pos()).Line
			confessional := commentLineWithinDistance(commentLines, callLine, 5)
			if !cfg.matchesMarker(methodName, recvType) && !confessional {
				return true
			}
			if confessional {
				reportAtf(pass, file, call.Pos(),
					"[RTA002] discarded call to method %q on %q with adjacent deadcode/RTA comment; this is a self-confessed reachability bypass",
					methodName, formatType(recvType))
				return true
			}
			reportAtf(pass, file, call.Pos(),
				"[RTA002] discarded call to project marker method %q on %q; if the call has no purpose other than counting as a use, drop the marker; if the receiver type really needs the interface, propagate the value through a real consumer",
				methodName, formatType(recvType))
			return true
		})
	}
	return nil, nil
}

type rtaSyntheticMarkerConfig struct {
	markerInterfaces map[string]struct{}
	markerMethods    map[string]struct{}
}

func loadRTASyntheticMarkerConfig(a *analysis.Analyzer) rtaSyntheticMarkerConfig {
	return rtaSyntheticMarkerConfig{
		markerInterfaces: csvFlagSet(&a.Flags, "marker_interfaces"),
		markerMethods:    csvFlagSet(&a.Flags, "marker_methods"),
	}
}

func (c rtaSyntheticMarkerConfig) matchesMarker(methodName string, recvType types.Type) bool {
	if _, ok := c.markerMethods[methodName]; ok {
		return true
	}
	if recvType == nil || len(c.markerInterfaces) == 0 {
		return false
	}
	for qualified := range c.markerInterfaces {
		if typeImplementsInterfaceByName(recvType, qualified) {
			return true
		}
	}
	return false
}

// discardedMethodCall returns the underlying CallExpr of a statement
// whose result is dropped. Accepted shapes:
//
//	_ = expr.Method()
//	var _ = expr.Method()
//	expr.Method()  (only when the method has a non-void return)
//
// The call must resolve to a method call (a SelectorExpr fun), not a
// free-function call. A void-returning call inside an *ast.ExprStmt
// is not a discard: nothing was discarded.
func discardedMethodCall(node ast.Node) (*ast.CallExpr, bool) {
	switch n := node.(type) {
	case *ast.AssignStmt:
		if !isUnderscoreAssign(n) {
			return nil, false
		}
		return callExprIfMethodCall(n.Rhs[0])
	case *ast.ExprStmt:
		call, ok := callExprIfMethodCall(n.X)
		if !ok {
			return nil, false
		}
		return call, true
	case *ast.ValueSpec:
		if !valueSpecIsUnderscoreOnly(n) {
			return nil, false
		}
		if len(n.Values) != 1 {
			return nil, false
		}
		return callExprIfMethodCall(n.Values[0])
	}
	return nil, false
}

func callHasResultValue(pass *analysis.Pass, call *ast.CallExpr) bool {
	if pass.TypesInfo == nil {
		return true
	}
	tv, ok := pass.TypesInfo.Types[call]
	if !ok || tv.Type == nil {
		return true
	}
	if tuple, isTuple := tv.Type.(*types.Tuple); isTuple {
		return tuple.Len() > 0
	}
	return true
}

func valueSpecIsUnderscoreOnly(spec *ast.ValueSpec) bool {
	if len(spec.Names) != 1 {
		return false
	}
	return spec.Names[0].Name == "_"
}

func isUnderscoreAssign(assign *ast.AssignStmt) bool {
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return false
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	return ok && ident.Name == "_"
}

func callExprIfMethodCall(expr ast.Expr) (*ast.CallExpr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	if _, ok := call.Fun.(*ast.SelectorExpr); !ok {
		return nil, false
	}
	return call, true
}

// resolveCalledMethod returns the method name and the receiver's
// concrete type. It uses pass.TypesInfo when available; if not, it
// falls back to a name-only resolution where the receiver type is nil
// and only marker_methods can match.
func resolveCalledMethod(pass *analysis.Pass, call *ast.CallExpr) (string, types.Type, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil, false
	}
	methodName := sel.Sel.Name
	if pass.TypesInfo == nil {
		return methodName, nil, true
	}
	if recvType := pass.TypesInfo.TypeOf(sel.X); recvType != nil {
		return methodName, recvType, true
	}
	return methodName, nil, true
}

// typeImplementsInterfaceByName checks whether t (or its pointer)
// has a method matching the named interface's method set. Because the
// interface type itself may not be loaded into TypesInfo, this
// approximates: split qualified into pkg + interface name and check
// only method names. Conservative: false negatives are preferable to
// false positives. Configured projects that want strict interface
// matching should use marker_methods.
func typeImplementsInterfaceByName(t types.Type, qualified string) bool {
	if t == nil {
		return false
	}
	dot := strings.LastIndex(qualified, ".")
	if dot < 0 {
		return false
	}
	pkgPath := qualified[:dot]
	ifaceName := qualified[dot+1:]
	named, ok := underlyingNamed(t)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	if pkgPath == obj.Pkg().Path() && obj.Name() == ifaceName {
		return true
	}
	for i := 0; i < named.NumMethods(); i++ {
		method := named.Method(i)
		if method == nil {
			continue
		}
		if method.Pkg() != nil && method.Pkg().Path() == pkgPath {
			return true
		}
	}
	return false
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

func formatType(t types.Type) string {
	if t == nil {
		return "(unknown type)"
	}
	return t.String()
}

func collectConfessionalCommentLines(fset *token.FileSet, file *ast.File) map[int]struct{} {
	lines := map[int]struct{}{}
	for _, group := range file.Comments {
		for _, c := range group.List {
			if rtaConfessionalCommentPattern.MatchString(c.Text) {
				lines[fset.Position(c.Pos()).Line] = struct{}{}
			}
		}
	}
	return lines
}

func commentLineWithinDistance(lines map[int]struct{}, target int, window int) bool {
	for line := range lines {
		if abs(line-target) <= window {
			return true
		}
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
