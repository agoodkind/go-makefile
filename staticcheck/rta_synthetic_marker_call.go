package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/analysis"
)

// RTASyntheticMarkerCallAnalyzer flags discarded method calls whose
// adjacent comments confess the bypass intent. The empirical case is:
//
//	// keep IsLivetrackMeta reachable for the deadcode analyzer
//	_ = SupervisorMeta{}.IsLivetrackMeta()
//
// The detector is intentionally narrow: it fires only when a comment
// matching (?i)deadcode|RTA|reachability sits within five lines of a
// discarded `_ = expr.Method()` (or `var _ = ...`, or an expression
// statement whose call returns at least one value). It catches sloppy
// bypasses that confess in writing; it does not pretend to catch a
// determined bypass author who omits the comment.
//
// A more aggressive shape-only mode (flagging any throwaway method
// call on a project type) was considered and rejected: it produces
// significant noise on real code and an attacker can sidestep it by
// adding an argument or by routing the call through a function. The
// real defence against an attacker silently editing lint config sits
// at the shell layer (agent-gate) rather than here.
//
// The diagnostic carries the literal "[RTA002]". Document intentional
// exceptions in the staticcheck-extra baseline, not via inline
// directives.
var RTASyntheticMarkerCallAnalyzer = newRTASyntheticMarkerCallAnalyzer()

func newRTASyntheticMarkerCallAnalyzer() *analysis.Analyzer {
	return &analysis.Analyzer{
		Name: "rta_synthetic_marker_call",
		Doc:  "rejects discarded method calls with adjacent deadcode/RTA/reachability comments",
		Run:  runRTASyntheticMarkerCall,
	}
}

var rtaConfessionalCommentPattern = regexp.MustCompile(`(?i)deadcode|\brta\b|reachability`)

func runRTASyntheticMarkerCall(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		commentLines := collectConfessionalCommentLines(pass.Fset, file)
		if len(commentLines) == 0 {
			continue
		}
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
			if !commentLineWithinDistance(commentLines, callLine, 5) {
				return true
			}
			reportAtf(pass, file, call.Pos(),
				"[RTA002] discarded call to method %q on %q with adjacent deadcode/RTA/reachability comment; this is a self-confessed reachability bypass. Drop the call and the alibi, or document why the call is load-bearing in a way that does not name the deadcode analyzer",
				methodName, formatType(recvType))
			return true
		})
	}
	return nil, nil
}

// discardedMethodCall returns the underlying CallExpr of a statement
// whose result is dropped. Accepted shapes:
//
//	_ = expr.Method()
//	var _ = expr.Method()
//	bare expression statement with a non-void method return
//
// The call must resolve to a method call (a SelectorExpr fun), not a
// free-function call. A void-returning call inside an [ast.ExprStmt]
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
// concrete type when TypesInfo is available; otherwise returns just
// the method name with a nil type.
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
