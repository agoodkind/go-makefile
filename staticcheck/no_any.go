package staticcheck

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// NoAnyOrEmptyInterfaceAnalyzer rejects loose `any`, `interface{}`, and any
// type alias or named type whose underlying shape is a bare empty interface
// or contains one as a leaf element of a map, slice, array, channel, or
// pointer.
//
// Two scopes apply:
//
//   - Type declarations (alias and named) are checked everywhere except
//     tests, generated code, and the analyzer's own source. The per-file
//     dynamic-boundary allowlist does not apply to type declarations
//     because the alias name escapes the file it was declared in. A
//     `type InputAttr = any` declared inside an allowlisted adapter file
//     spreads through every signature that names `InputAttr`, which is
//     functionally identical to writing `any` in those signatures.
//
//   - Function parameters and return types are checked everywhere except
//     tests, generated code, the analyzer's own source, and files in the
//     per-file dynamic-boundary allowlist. Adapter code that bridges to
//     a genuinely dynamic upstream protocol may use `any` directly inside
//     the allowlisted file; it may not declare type aliases that propagate
//     `any` outside it.
var NoAnyOrEmptyInterfaceAnalyzer = &analysis.Analyzer{
	Name: "no_any_or_empty_interface",
	Doc:  "rejects any, interface{}, and aliases or named types that expand to them",
	Run:  runNoAnyOrEmptyInterface,
}

func runNoAnyOrEmptyInterface(pass *analysis.Pass) (any, error) {
	if isStaticcheckPackage(pass) {
		return nil, nil
	}
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		walkFileForBannedShapes(pass, file, allowsDynamicBoundary(path))
	}
	return nil, nil
}

// walkFileForBannedShapes traverses every node in file and dispatches each
// kind to its specific check helper. Splitting per-node-kind keeps each
// branch readable and the cognitive complexity inside the inspector low.
func walkFileForBannedShapes(pass *analysis.Pass, file *ast.File, allowSignatureAny bool) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.TypeSpec:
			checkDeclaredType(pass, node)
		case *ast.FuncType:
			if !allowSignatureAny {
				checkFuncTypeSignature(pass, node)
			}
		case *ast.ValueSpec:
			if node.Type != nil {
				checkSignatureExpr(pass, node.Type)
			}
		case *ast.CompositeLit:
			if node.Type != nil {
				checkSignatureExpr(pass, node.Type)
			}
		case *ast.TypeAssertExpr:
			if node.Type != nil {
				checkSignatureExpr(pass, node.Type)
			}
		}
		return true
	})
}

// checkFuncTypeSignature walks both params and results of a FuncType and
// reports any banned shape. Catches top-level funcs, methods, interface
// methods, function-value fields, function literals, and closures.
// Subject to the per-file dynamic-boundary allowlist at the call site.
func checkFuncTypeSignature(pass *analysis.Pass, ft *ast.FuncType) {
	if ft.Params != nil {
		for _, p := range ft.Params.List {
			checkSignatureExpr(pass, p.Type)
		}
	}
	if ft.Results != nil {
		for _, r := range ft.Results.List {
			checkSignatureExpr(pass, r.Type)
		}
	}
}

// checkDeclaredType inspects a type declaration. It catches both literal
// `any` / `interface{}` written in the type expression and aliases or named
// types whose underlying shape resolves to a forbidden composition. Struct
// field types are walked separately so a struct literal-declaring an `any`
// field gets a per-field report.
func checkDeclaredType(pass *analysis.Pass, spec *ast.TypeSpec) {
	astCheckExpr(pass, spec.Type)
	if t := pass.TypesInfo.TypeOf(spec.Type); t != nil {
		if reason := bannedReason(t); reason != "" {
			pass.Reportf(spec.Pos(),
				"type %s expands to %s, which is forbidden; define a deeply enumerated named type instead",
				spec.Name.Name, reason)
		}
	}
	if structType, ok := spec.Type.(*ast.StructType); ok && structType.Fields != nil {
		for _, field := range structType.Fields.List {
			ft := pass.TypesInfo.TypeOf(field.Type)
			if ft == nil {
				continue
			}
			if reason := bannedReason(ft); reason != "" {
				pass.Reportf(field.Pos(),
					"struct field type %s expands to %s; define a deeply enumerated named type",
					types.ExprString(field.Type), reason)
			}
		}
	}
}

// checkSignatureExpr inspects a function parameter or return type. It runs
// both an AST scan for literal `any` / `interface{}` and a types-system scan
// that follows aliases. The types-system scan ensures a signature that uses
// a named alias whose underlying is forbidden gets reported.
func checkSignatureExpr(pass *analysis.Pass, expr ast.Expr) {
	astCheckExpr(pass, expr)
	if t := pass.TypesInfo.TypeOf(expr); t != nil {
		if reason := bannedReason(t); reason != "" {
			pass.Reportf(expr.Pos(),
				"signature uses %s, which expands to %s; define a deeply enumerated named type",
				types.ExprString(expr), reason)
		}
	}
}

// astCheckExpr reports literal `any` identifiers and bare `interface{}`
// types found anywhere in expr. Catches the case before TypesInfo runs and
// keeps the report attached to the source position the author wrote.
func astCheckExpr(pass *analysis.Pass, expr ast.Expr) {
	ast.Inspect(expr, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			if typed.Name == "any" {
				pass.Reportf(typed.Pos(), "do not use any; define a deeply enumerated named type")
			}
		case *ast.InterfaceType:
			if len(typed.Methods.List) == 0 {
				pass.Reportf(typed.Pos(), "do not use interface{}; define a deeply enumerated named type")
			}
		}
		return true
	})
}

// bannedReason returns a non-empty description if t is or contains a bare
// empty interface as a leaf element. The walk follows underlying types
// through aliases, named types, maps, slices, arrays, channels, and
// pointers. It stops at struct boundaries so struct field reporting is
// done by checkDeclaredType.
func bannedReason(t types.Type) string {
	if t == nil {
		return ""
	}
	switch x := t.Underlying().(type) {
	case *types.Interface:
		if x.NumMethods() == 0 && x.NumEmbeddeds() == 0 {
			return "any (empty interface)"
		}
		for i := range x.NumEmbeddeds() {
			if r := bannedReason(x.EmbeddedType(i)); r != "" {
				return "interface embedding " + r
			}
		}
		return ""
	case *types.Map:
		if r := bannedReason(x.Key()); r != "" {
			return "map with key " + r
		}
		if r := bannedReason(x.Elem()); r != "" {
			return "map with value " + r
		}
		return ""
	case *types.Slice:
		if r := bannedReason(x.Elem()); r != "" {
			return "slice of " + r
		}
		return ""
	case *types.Array:
		if r := bannedReason(x.Elem()); r != "" {
			return "array of " + r
		}
		return ""
	case *types.Chan:
		if r := bannedReason(x.Elem()); r != "" {
			return "channel of " + r
		}
		return ""
	case *types.Pointer:
		if r := bannedReason(x.Elem()); r != "" {
			return "pointer to " + r
		}
		return ""
	}
	return ""
}

func allowsDynamicBoundary(path string) bool {
	allowed := []string{
		"internal/adapter/codex/backend.go",
		"internal/adapter/codex/continuation.go",
		"internal/adapter/codex/native_tools.go",
		"internal/adapter/codex/protocol.go",
		"internal/adapter/codex/request_builder.go",
		"internal/adapter/codex/transport_request.go",
		"internal/adapter/codex/transport_ws.go",
		"internal/adapter/codex/ws_session.go",
		"internal/adapter/codex/delta_input.go",
		"internal/adapter/openai/",
	}
	for _, item := range allowed {
		if strings.Contains(path, item) {
			return true
		}
	}
	return false
}
