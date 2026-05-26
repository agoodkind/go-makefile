package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// GrpcMethodNameLiteralAnalyzer flags call sites where a function
// expects a gRPC full method name (a typed proto-generated constant
// such as `clydev1.ClydeService_SubscribeRegistry_FullMethodName`) but
// the caller passes a bare string literal instead. The empirical bug is:
//
//	r.RelayStream(stream, "SubscribeRegistry", ...)
//
// where the method name is supposed to be the proto-generated constant
//
//	r.RelayStream(stream, clydev1.ClydeService_SubscribeRegistry_FullMethodName, ...)
//
// A bare literal silently desynchronises from the proto contract: the
// string survives a rename of the RPC, and routing/relay code that
// dispatches on the full method name will quietly stop matching.
//
// The detector is intentionally narrow and opt-in. It does not try to
// flag every string-literal-shaped argument in the codebase. Consumers
// configure the exact call sites they want to monitor via the
// `-targets` flag.
//
// Configurable behavior via analyzer flags:
//
//	-targets  comma-separated list of target call sites in one of two
//	          shapes:
//	            <pkg>.<Type>.<Method>:<argIndex>
//	            <pkg>.<Function>:<argIndex>
//	          Examples:
//	            goodkind.io/clyde/internal/clydesup.GRPCRelay.RelayStream:1
//	            goodkind.io/clyde/internal/clydesup.RouteUnary:0
//	          The argIndex is zero-based and identifies which positional
//	          argument is supposed to be the proto-generated full method
//	          name. The package path is the canonical Go import path for
//	          the function or method's receiver type.
//
// Detection contract for each target call site:
//
//  1. If the configured argument is a bare string literal
//     (*ast.BasicLit of kind STRING), report.
//  2. If the argument references a named constant whose identifier
//     matches the proto-generated pattern (ends in `_FullMethodName`),
//     do not report.
//  3. If the argument is anything else (variable, function call result,
//     composite expression, named constant that does not match the
//     pattern), do not report unless the argument is a bare string
//     literal. The detector is for the specific bare-literal mistake;
//     sophisticated indirection is out of scope.
//
// Knowingly admitted false negatives:
//
//   - A constant named something other than `*_FullMethodName` that
//     contains the same string value. The detector treats any non-literal
//     reference as acceptable and does not chase constant values.
//   - A literal hidden inside a composite expression (concatenation,
//     conversion, function call). Out of scope by design.
//   - Variadic call shapes where the target argument index lands in the
//     variadic slice. The detector matches by positional index.
//
// The diagnostic carries the literal "[GRPCMETHOD001]" so consumers can
// grep it in CI output. The default targets list is empty; consumers
// must opt in for their own call sites.
var GrpcMethodNameLiteralAnalyzer = newGrpcMethodNameLiteralAnalyzer()

func newGrpcMethodNameLiteralAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name: "grpc_method_name_literal",
		Doc:  "rejects bare string literals at call sites that expect a proto-generated gRPC _FullMethodName constant",
		Run:  runGrpcMethodNameLiteral,
	}
	a.Flags.String("targets", "",
		"comma-separated list of target call sites; each entry is "+
			"<pkg>.<Type>.<Method>:<argIndex> or <pkg>.<Function>:<argIndex>")
	return a
}

type grpcMethodNameTarget struct {
	pkgPath  string
	typeName string
	funcName string
	argIndex int
}

func (t grpcMethodNameTarget) isMethod() bool {
	return t.typeName != ""
}

func runGrpcMethodNameLiteral(pass *analysis.Pass) (any, error) {
	targets := loadGrpcMethodNameTargets(pass.Analyzer)
	if len(targets) == 0 {
		return nil, nil
	}
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			matched, target, ok := matchGrpcMethodNameTarget(pass, call, targets)
			if !ok {
				return true
			}
			if target.argIndex < 0 || target.argIndex >= len(call.Args) {
				return true
			}
			arg := call.Args[target.argIndex]
			lit, isLit := arg.(*ast.BasicLit)
			if !isLit {
				return true
			}
			if lit.Kind != token.STRING {
				return true
			}
			value := lit.Value
			unquoted, err := strconv.Unquote(value)
			if err == nil {
				value = unquoted
			}
			reportAtf(pass, file, lit.Pos(),
				"[GRPCMETHOD001] grpc method name should be a proto-generated _FullMethodName constant; got bare string literal %q at %s argument index %d",
				value, matched, target.argIndex)
			return true
		})
	}
	return nil, nil
}

func loadGrpcMethodNameTargets(a *analysis.Analyzer) []grpcMethodNameTarget {
	raw := csvFlagSet(&a.Flags, "targets")
	targets := make([]grpcMethodNameTarget, 0, len(raw))
	for entry := range raw {
		target, ok := parseGrpcMethodNameTarget(entry)
		if !ok {
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

// parseGrpcMethodNameTarget parses one target entry. Accepted shapes:
//
//	<pkg>.<Type>.<Method>:<argIndex>
//	<pkg>.<Function>:<argIndex>
//
// The package path may itself contain dots (e.g.
// `goodkind.io/clyde/internal/clydesup`). The boundary between the
// package path and the rest is the last slash; everything after the
// last slash is then split on dots. A two-part tail names a function;
// a three-part tail names a Type.Method pair.
func parseGrpcMethodNameTarget(entry string) (grpcMethodNameTarget, bool) {
	colonIdx := strings.LastIndex(entry, ":")
	if colonIdx < 0 {
		return grpcMethodNameTarget{}, false
	}
	head := entry[:colonIdx]
	tail := entry[colonIdx+1:]
	argIndex, err := strconv.Atoi(tail)
	if err != nil || argIndex < 0 {
		return grpcMethodNameTarget{}, false
	}
	dirPart, lastSegment, hasSlash := splitGrpcMethodNameHead(head)
	if !hasSlash {
		lastSegment = head
	}
	parts := strings.Split(lastSegment, ".")
	pkgPrefix := ""
	if hasSlash {
		pkgPrefix = dirPart + "/"
	}
	switch len(parts) {
	case 2:
		return grpcMethodNameTarget{
			pkgPath:  pkgPrefix + parts[0],
			funcName: parts[1],
			argIndex: argIndex,
		}, true
	case 3:
		return grpcMethodNameTarget{
			pkgPath:  pkgPrefix + parts[0],
			typeName: parts[1],
			funcName: parts[2],
			argIndex: argIndex,
		}, true
	}
	return grpcMethodNameTarget{}, false
}

// splitGrpcMethodNameHead splits a head like
// `goodkind.io/clyde/internal/clydesup.GRPCRelay.RelayStream` into its
// directory portion (`goodkind.io/clyde/internal`) and the last
// segment (`clydesup.GRPCRelay.RelayStream`). When there is no slash
// the whole head is returned as the last segment with an empty
// directory and the second return is false; the caller falls back to
// dotted parsing in that case.
func splitGrpcMethodNameHead(head string) (string, string, bool) {
	slashIdx := strings.LastIndex(head, "/")
	if slashIdx < 0 {
		return "", head, false
	}
	return head[:slashIdx], head[slashIdx+1:], true
}

// matchGrpcMethodNameTarget tries to match the call against any
// configured target. It returns the matched target's display string,
// the matched target, and ok=true. Resolution prefers TypesInfo. When
// TypesInfo is unavailable (e.g. parse-only callers), the matcher
// degrades gracefully and returns ok=false rather than guessing.
func matchGrpcMethodNameTarget(pass *analysis.Pass, call *ast.CallExpr, targets []grpcMethodNameTarget) (string, grpcMethodNameTarget, bool) {
	if pass.TypesInfo == nil {
		return "", grpcMethodNameTarget{}, false
	}
	pkgPath, typeName, funcName, ok := resolveCallIdentity(pass, call)
	if !ok {
		return "", grpcMethodNameTarget{}, false
	}
	for _, target := range targets {
		if target.funcName != funcName {
			continue
		}
		if target.pkgPath != pkgPath {
			continue
		}
		if target.isMethod() {
			if typeName == "" || !typeNameMatches(typeName, target.typeName) {
				continue
			}
		} else if typeName != "" {
			continue
		}
		display := target.pkgPath + "." + funcName
		if target.isMethod() {
			display = target.pkgPath + "." + target.typeName + "." + funcName
		}
		return display, target, true
	}
	return "", grpcMethodNameTarget{}, false
}

// resolveCallIdentity returns the package path, optional receiver type
// name (without pointer or package qualifier), and function/method
// name that the call resolves to under TypesInfo. Returns ok=false
// when the call cannot be resolved (built-ins, untyped expressions,
// dynamic dispatch through an interface that the analyzer cannot
// pinpoint to a concrete declaration, etc.).
func resolveCallIdentity(pass *analysis.Pass, call *ast.CallExpr) (string, string, string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return resolveIdentCall(pass, fun)
	case *ast.SelectorExpr:
		return resolveSelectorCall(pass, fun)
	}
	return "", "", "", false
}

func resolveIdentCall(pass *analysis.Pass, ident *ast.Ident) (string, string, string, bool) {
	fnObj, ok := lookupFuncObject(pass, ident)
	if !ok {
		return "", "", "", false
	}
	return identityFromFuncObject(fnObj)
}

func resolveSelectorCall(pass *analysis.Pass, sel *ast.SelectorExpr) (string, string, string, bool) {
	if selection := pass.TypesInfo.Selections[sel]; selection != nil {
		return identityFromSelection(selection)
	}
	fnObj, ok := lookupFuncObject(pass, sel.Sel)
	if !ok {
		return "", "", "", false
	}
	return identityFromFuncObject(fnObj)
}

func identityFromSelection(selection *types.Selection) (string, string, string, bool) {
	if selection.Kind() != types.MethodVal && selection.Kind() != types.MethodExpr {
		return "", "", "", false
	}
	fnObj, ok := selection.Obj().(*types.Func)
	if !ok || fnObj.Pkg() == nil {
		return "", "", "", false
	}
	recvName, recvOK := receiverTypeBaseName(selection.Recv())
	if !recvOK {
		return "", "", "", false
	}
	return fnObj.Pkg().Path(), recvName, fnObj.Name(), true
}

func identityFromFuncObject(fnObj *types.Func) (string, string, string, bool) {
	sig, ok := fnObj.Type().(*types.Signature)
	if !ok {
		return "", "", "", false
	}
	recv := sig.Recv()
	if recv == nil {
		return fnObj.Pkg().Path(), "", fnObj.Name(), true
	}
	recvName, recvOK := receiverTypeBaseName(recv.Type())
	if !recvOK {
		return "", "", "", false
	}
	return fnObj.Pkg().Path(), recvName, fnObj.Name(), true
}

func lookupFuncObject(pass *analysis.Pass, ident *ast.Ident) (*types.Func, bool) {
	obj := pass.TypesInfo.Uses[ident]
	if obj == nil {
		obj = pass.TypesInfo.Defs[ident]
	}
	fnObj, ok := obj.(*types.Func)
	if !ok || fnObj.Pkg() == nil {
		return nil, false
	}
	return fnObj, true
}

// receiverTypeBaseName strips the pointer and any package qualifier
// from a receiver type, returning the bare type name (e.g. "GRPCRelay"
// from "*goodkind.io/clyde/internal/clydesup.GRPCRelay"). Returns
// ok=false for receiver types that have no usable name (interface
// literals, etc.).
func receiverTypeBaseName(t types.Type) (string, bool) {
	if t == nil {
		return "", false
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return "", false
	}
	if obj := named.Obj(); obj != nil {
		return obj.Name(), true
	}
	return "", false
}

// typeNameMatches compares the resolved receiver name against the
// configured target type name. Both are bare (no pointer, no package
// qualifier).
func typeNameMatches(resolved string, configured string) bool {
	return resolved == configured
}
