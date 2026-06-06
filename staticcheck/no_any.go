package staticcheck

import (
	"go/ast"
	"go/build"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// NoAnyOrEmptyInterfaceAnalyzer rejects loose `any`, `interface{}`, and any
// type alias or named type whose underlying shape is a bare empty interface
// or contains one as a leaf element of a map, slice, array, channel, or
// pointer.
//
// There is no per-file or per-package allowlist. Every Go file in the
// project (excluding tests, generated code, and the analyzer's own source)
// is checked uniformly. Files that legitimately need to handle dynamic
// payloads at the protocol boundary must do so behind a typed adapter
// (json.RawMessage, a sealed interface marker, a deeply enumerated
// struct shape) rather than by passing `any` through internal helpers.
//
// The historical per-file allowlist was removed because it enabled
// helper-extraction laundering: an LLM could split a complex function
// into smaller helpers inside an allowlisted file, drive cyclomatic
// complexity numbers down, and silently spread `any` through the new
// helpers. With no allowlist, every helper that takes or returns `any`
// must justify itself in code review, which is visible.
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
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		walkFileForBannedShapes(pass, file)
	}
	return nil, nil
}

// walkFileForBannedShapes traverses every node in file and dispatches each
// kind to its specific check helper. Splitting per-node-kind keeps each
// branch readable and the cognitive complexity inside the inspector low.
func walkFileForBannedShapes(pass *analysis.Pass, file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.TypeSpec:
			checkDeclaredType(pass, file, node)
		case *ast.FuncType:
			checkFuncTypeSignature(pass, file, node)
		case *ast.ValueSpec:
			if node.Type != nil {
				checkSignatureExpr(pass, file, node.Type)
			}
		case *ast.CompositeLit:
			if node.Type != nil {
				checkSignatureExpr(pass, file, node.Type)
			}
		case *ast.TypeAssertExpr:
			if node.Type != nil {
				checkSignatureExpr(pass, file, node.Type)
			}
		}
		return true
	})
}

// checkFuncTypeSignature walks both params and results of a FuncType and
// reports any banned shape. Catches top-level funcs, methods, interface
// methods, function-value fields, function literals, and closures.
// Applied uniformly across every non-test, non-generated file.
func checkFuncTypeSignature(pass *analysis.Pass, file *ast.File, ft *ast.FuncType) {
	if ft.Params != nil {
		for _, p := range ft.Params.List {
			checkSignatureExpr(pass, file, p.Type)
		}
	}
	if ft.Results != nil {
		for _, r := range ft.Results.List {
			checkSignatureExpr(pass, file, r.Type)
		}
	}
}

// checkDeclaredType inspects a type declaration. It catches both literal
// `any` / `interface{}` written in the type expression and aliases or named
// types whose underlying shape resolves to a forbidden composition. Struct
// field types are walked separately so a struct literal-declaring an `any`
// field gets a per-field report.
func checkDeclaredType(pass *analysis.Pass, file *ast.File, spec *ast.TypeSpec) {
	astCheckExpr(pass, file, spec.Type)
	if t := pass.TypesInfo.TypeOf(spec.Type); t != nil {
		if reason := bannedReason(pass, t); reason != "" {
			reportAtf(pass, file, spec.Pos(),
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
			if reason := bannedReason(pass, ft); reason != "" {
				reportAtf(pass, file, field.Pos(),
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
func checkSignatureExpr(pass *analysis.Pass, file *ast.File, expr ast.Expr) {
	astCheckExpr(pass, file, expr)
	if t := pass.TypesInfo.TypeOf(expr); t != nil {
		if reason := bannedReason(pass, t); reason != "" {
			reportAtf(pass, file, expr.Pos(),
				"signature uses %s, which expands to %s; define a deeply enumerated named type",
				types.ExprString(expr), reason)
		}
	}
}

// astCheckExpr reports literal `any` identifiers and bare `interface{}`
// types found anywhere in expr. Catches the case before TypesInfo runs and
// keeps the report attached to the source position the author wrote.
func astCheckExpr(pass *analysis.Pass, file *ast.File, expr ast.Expr) {
	ast.Inspect(expr, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			if typed.Name == "any" {
				reportAtf(pass, file, typed.Pos(), "do not use any; define a deeply enumerated named type")
			}
		case *ast.InterfaceType:
			if len(typed.Methods.List) == 0 {
				reportAtf(pass, file, typed.Pos(), "do not use interface{}; define a deeply enumerated named type")
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
//
// A named type or alias declared in a dependency or in the standard library
// is never reported. The consumer did not author that type; it only named
// it. tea.Msg, whose underlying is interface{}, is mandated by the bubbletea
// Model interface, and [database/sql/driver.Value] is `type Value any`. The
// anti-laundering intent applies only to types declared inside the analyzed
// module, whose source files live in the working tree rather than under the
// module cache, GOROOT, or a vendor directory.
func bannedReason(pass *analysis.Pass, t types.Type) string {
	if t == nil {
		return ""
	}
	if obj := typeDefiningObject(t); obj != nil && objectIsExternal(pass, obj) {
		return ""
	}
	switch x := t.Underlying().(type) {
	case *types.Interface:
		return interfaceBannedReason(pass, x)
	case *types.Map:
		return mapBannedReason(pass, x)
	case *types.Slice:
		if r := bannedReason(pass, x.Elem()); r != "" {
			return "slice of " + r
		}
	case *types.Array:
		if r := bannedReason(pass, x.Elem()); r != "" {
			return "array of " + r
		}
	case *types.Chan:
		if r := bannedReason(pass, x.Elem()); r != "" {
			return "channel of " + r
		}
	case *types.Pointer:
		if r := bannedReason(pass, x.Elem()); r != "" {
			return "pointer to " + r
		}
	}
	return ""
}

// interfaceBannedReason reports the empty-interface reason for an interface
// underlying type: the bare empty interface itself, or an embedded interface
// that expands to one.
func interfaceBannedReason(pass *analysis.Pass, x *types.Interface) string {
	if x.NumMethods() == 0 && x.NumEmbeddeds() == 0 {
		return "any (empty interface)"
	}
	for embedded := range x.EmbeddedTypes() {
		if r := bannedReason(pass, embedded); r != "" {
			return "interface embedding " + r
		}
	}
	return ""
}

// mapBannedReason reports the empty-interface reason for a map underlying type,
// checking the key then the value.
func mapBannedReason(pass *analysis.Pass, x *types.Map) string {
	if r := bannedReason(pass, x.Key()); r != "" {
		return "map with key " + r
	}
	if r := bannedReason(pass, x.Elem()); r != "" {
		return "map with value " + r
	}
	return ""
}

// typeDefiningObject returns the TypeName that declares t when t is a named
// type or an alias, and nil otherwise. It locates the source file that
// declares the type so dependency and standard-library types can be exempted.
func typeDefiningObject(t types.Type) *types.TypeName {
	switch x := t.(type) {
	case *types.Named:
		return x.Obj()
	case *types.Alias:
		return x.Obj()
	}
	return nil
}

// objectIsExternal reports whether obj is declared outside the analyzed
// module: in a dependency under the module cache, in the standard library
// under GOROOT, or under a vendor directory. A builtin or universe type
// (obj.Pkg() == nil, such as error) is treated as in-repo so existing
// behavior is unchanged.
func objectIsExternal(pass *analysis.Pass, obj *types.TypeName) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	pos := obj.Pos()
	if !pos.IsValid() {
		return false
	}
	return fileIsExternalDependency(pass.Fset.Position(pos).Filename)
}

// fileIsExternalDependency reports whether path lives in the module cache,
// the standard library tree, or a vendor directory. The substring checks
// mirror the path heuristic style used by isGoBuildCacheDescriptorPath.
func fileIsExternalDependency(path string) bool {
	if path == "" {
		return false
	}
	slash := filepath.ToSlash(path)
	if strings.Contains(slash, "/vendor/") || strings.Contains(slash, "/pkg/mod/") {
		return true
	}
	goroot := build.Default.GOROOT
	if goroot == "" {
		return false
	}
	return pathIsWithin(slash, filepath.ToSlash(filepath.Clean(goroot)))
}
