package staticcheck

import (
	"flag"
	"go/ast"
	"go/token"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// RTAThrowawayRegistrationAnalyzer flags a deadcode RTA bypass shape
// where a server is constructed only so a Register*Server call drags
// the receiver type's method set into the SSA call graph, after which
// the server is dropped on the floor. The empirical case is
// `grpc.NewServer()` followed by `RegisterClydeServiceServer(grpcServer, relay)`
// inside a function that returns the relay (not the server) and whose
// only caller discards the result.
//
// The detector does not require type information. It walks every
// function body, looks for variables assigned from a *NewServer call,
// and flags the assignment if the variable is only used as the first
// argument to a Register* call and never:
//
//   - returned from the enclosing function
//   - passed to Serve / ListenAndServe / ServeTLS / Shutdown
//   - assigned to a struct field
//   - sent on a channel
//
// The diagnostic carries the literal "[RTA003]" so consumers can grep
// it in CI output.
//
// Configurable behavior via analyzer flags:
//
//	-server_constructors  comma-separated list of qualified call names
//	                      whose return is a "server" (e.g. "grpc.NewServer").
//	                      Empty means: any selector call whose Sel ends in
//	                      "NewServer".
//	-registration_funcs   comma-separated list of registration call names.
//	                      Empty means: any call whose name matches
//	                      ^Register.*Server$.
//
// Document intentional exceptions in the staticcheck-extra baseline,
// not via inline directives.
var RTAThrowawayRegistrationAnalyzer = newRTAThrowawayRegistrationAnalyzer()

func newRTAThrowawayRegistrationAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name: "rta_throwaway_registration",
		Doc:  "rejects servers constructed only to be passed to a Register*Server call and then discarded",
		Run:  runRTAThrowawayRegistration,
	}
	a.Flags.String("server_constructors", "", "comma-separated qualified call names that return a server (e.g. grpc.NewServer)")
	a.Flags.String("registration_funcs", "", "comma-separated qualified call names that register a service on a server (e.g. clydev1.RegisterClydeServiceServer)")
	return a
}

var registerServerCallPattern = regexp.MustCompile(`^Register.*Server$`)

func runRTAThrowawayRegistration(pass *analysis.Pass) (any, error) {
	cfg := loadRTAThrowawayConfig(pass.Analyzer)
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			checkRTAThrowawayInFunc(pass, file, fn, cfg)
		}
	}
	return nil, nil
}

type rtaThrowawayConfig struct {
	serverConstructors map[string]struct{}
	registrationFuncs  map[string]struct{}
}

func loadRTAThrowawayConfig(a *analysis.Analyzer) rtaThrowawayConfig {
	cfg := rtaThrowawayConfig{
		serverConstructors: csvFlagSet(&a.Flags, "server_constructors"),
		registrationFuncs:  csvFlagSet(&a.Flags, "registration_funcs"),
	}
	return cfg
}

func checkRTAThrowawayInFunc(pass *analysis.Pass, file *ast.File, fn *ast.FuncDecl, cfg rtaThrowawayConfig) {
	for _, stmt := range fn.Body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE {
			continue
		}
		varName, callName, ok := serverConstructorAssignment(assign, cfg)
		if !ok {
			continue
		}
		usage := classifyServerVarUsage(fn.Body, varName, fn.Body.Pos(), assign.End(), cfg)
		if usage.servedOrReturned {
			continue
		}
		if !usage.registered {
			continue
		}
		reportAtf(pass, file, assign.Pos(),
			"[RTA003] %q is constructed via %s and only passed to a Register*Server call; the server is never served, returned, or stored. This is a deadcode RTA bypass: drop the registration if the server is unused, or call Serve/ListenAndServe on it",
			varName, callName)
	}
}

// serverConstructorAssignment recognises `name := pkg.NewServer(...)` or
// any selector call whose Sel ends in "NewServer", returning the
// declared variable name and the qualified call name. If the assignment
// declares multiple variables, none match.
func serverConstructorAssignment(assign *ast.AssignStmt, cfg rtaThrowawayConfig) (varName string, callName string, ok bool) {
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return "", "", false
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" {
		return "", "", false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", "", false
	}
	qualified, ok := qualifiedCallName(call)
	if !ok {
		return "", "", false
	}
	if !isServerConstructor(qualified, cfg) {
		return "", "", false
	}
	return ident.Name, qualified, true
}

func isServerConstructor(qualified string, cfg rtaThrowawayConfig) bool {
	if len(cfg.serverConstructors) > 0 {
		_, hit := cfg.serverConstructors[qualified]
		return hit
	}
	short := qualified
	if dot := strings.LastIndex(qualified, "."); dot >= 0 {
		short = qualified[dot+1:]
	}
	return strings.HasSuffix(short, "NewServer")
}

func isRegistrationFunc(qualified string, cfg rtaThrowawayConfig) bool {
	if len(cfg.registrationFuncs) > 0 {
		_, hit := cfg.registrationFuncs[qualified]
		return hit
	}
	short := qualified
	if dot := strings.LastIndex(qualified, "."); dot >= 0 {
		short = qualified[dot+1:]
	}
	return registerServerCallPattern.MatchString(short)
}

type serverVarUsage struct {
	registered       bool
	servedOrReturned bool
}

// classifyServerVarUsage walks every reference to varName inside body
// after the declaration position and classifies the references. A
// reference counts as "registered" when it is the first argument of a
// Register*Server call. A reference counts as "served or returned" if
// it appears in a return statement, is the receiver of Serve /
// ListenAndServe / ServeTLS / Shutdown / GracefulStop, is assigned to a
// struct field, is sent on a channel, or is captured by a closure that
// is launched via go or saved beyond the function scope.
func classifyServerVarUsage(body *ast.BlockStmt, varName string, _ token.Pos, declEnd token.Pos, cfg rtaThrowawayConfig) serverVarUsage {
	usage := serverVarUsage{}
	parents := buildParentMap(body)
	ast.Inspect(body, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != varName {
			return true
		}
		if ident.Pos() < declEnd {
			return true
		}
		usage.recordReference(ident, parents, cfg)
		return true
	})
	return usage
}

func (u *serverVarUsage) recordReference(ident *ast.Ident, parents map[ast.Node]ast.Node, cfg rtaThrowawayConfig) {
	parent := parents[ident]
	if parent == nil {
		return
	}
	switch p := parent.(type) {
	case *ast.CallExpr:
		u.recordCallReference(ident, p, parents, cfg)
	case *ast.SelectorExpr:
		u.recordSelectorReference(ident, p, parents)
	case *ast.ReturnStmt:
		u.servedOrReturned = true
	case *ast.AssignStmt:
		if isStructFieldLHS(p) {
			u.servedOrReturned = true
		}
	case *ast.SendStmt:
		u.servedOrReturned = true
	case *ast.KeyValueExpr:
		u.servedOrReturned = true
	}
}

func (u *serverVarUsage) recordCallReference(ident *ast.Ident, call *ast.CallExpr, parents map[ast.Node]ast.Node, cfg rtaThrowawayConfig) {
	qualified, qualifiedOK := qualifiedCallName(call)
	if !qualifiedOK {
		return
	}
	if isRegistrationFunc(qualified, cfg) && len(call.Args) > 0 && call.Args[0] == ident {
		u.registered = true
		return
	}
	short := qualified
	if dot := strings.LastIndex(qualified, "."); dot >= 0 {
		short = qualified[dot+1:]
	}
	switch short {
	case "Serve", "ListenAndServe", "ServeTLS", "ListenAndServeTLS", "Shutdown", "GracefulStop", "Stop":
		if isReceiverOfCall(ident, call) {
			u.servedOrReturned = true
		}
	}
	_ = parents
}

func (u *serverVarUsage) recordSelectorReference(ident *ast.Ident, sel *ast.SelectorExpr, parents map[ast.Node]ast.Node) {
	if sel.X != ident {
		return
	}
	gp := parents[sel]
	if call, ok := gp.(*ast.CallExpr); ok && call.Fun == sel {
		switch sel.Sel.Name {
		case "Serve", "ListenAndServe", "ServeTLS", "ListenAndServeTLS", "Shutdown", "GracefulStop", "Stop":
			u.servedOrReturned = true
		}
	}
}

func isReceiverOfCall(ident *ast.Ident, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.X == ident
}

func isStructFieldLHS(assign *ast.AssignStmt) bool {
	for _, lhs := range assign.Lhs {
		if _, ok := lhs.(*ast.SelectorExpr); ok {
			return true
		}
	}
	return false
}

// qualifiedCallName returns "pkg.Name" for `pkg.Name(...)` and "Name"
// for unqualified calls like `Name(...)`. Method calls on an arbitrary
// expression return just the method name.
func qualifiedCallName(call *ast.CallExpr) (string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name, true
	case *ast.SelectorExpr:
		if recv, ok := fun.X.(*ast.Ident); ok {
			return recv.Name + "." + fun.Sel.Name, true
		}
		return fun.Sel.Name, true
	}
	return "", false
}

// csvFlagSet reads a comma-separated string flag from fs and returns
// the trimmed non-empty entries as a set. Missing or empty flags
// produce an empty set, which callers interpret as "use built-in
// defaults".
func csvFlagSet(fs *flag.FlagSet, name string) map[string]struct{} {
	out := map[string]struct{}{}
	if fs == nil {
		return out
	}
	f := fs.Lookup(name)
	if f == nil {
		return out
	}
	val := f.Value.String()
	if val == "" {
		return out
	}
	for item := range strings.SplitSeq(val, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out[trimmed] = struct{}{}
		}
	}
	return out
}
