package staticcheck

import (
	"go/ast"
	"go/types"
	"slices"

	"golang.org/x/tools/go/analysis"
)

// dictatedSignatures returns the function signatures in the package whose
// shape a type declared in another module fixes. The author cannot change such
// a signature without breaking the contract it satisfies, so an `any` in it is
// not the author's choice.
//
// Two shapes qualify:
//
//   - A method whose receiver type implements an interface declared in another
//     module, when the interface declares a method of the same name. Interface
//     satisfaction requires the whole method signature to match.
//   - A function literal or a function declared in this package that is used as
//     a value of a named function type declared in another module: returned,
//     assigned, passed as an argument, converted, or placed in a composite
//     literal where that named type is expected. A free function that merely
//     shares the signature, without such a use, does not qualify.
func dictatedSignatures(pass *analysis.Pass) map[*ast.FuncType]bool {
	dictated := map[*ast.FuncType]bool{}
	if pass.TypesInfo == nil || pass.Pkg == nil {
		return dictated
	}
	declByFunc := map[*types.Func]*ast.FuncDecl{}
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			method, ok := pass.TypesInfo.Defs[fn.Name].(*types.Func)
			if !ok {
				continue
			}
			declByFunc[method] = fn
			if fn.Recv != nil && methodImplementsExternalInterface(pass, method) {
				dictated[fn.Type] = true
			}
		}
	}
	for _, file := range pass.Files {
		forEachExpectedType(pass, file, func(expr ast.Expr, target types.Type) {
			if funcType := funcTypeUsedAs(pass, declByFunc, expr, target); funcType != nil {
				dictated[funcType] = true
			}
		})
	}
	return dictated
}

// funcTypeUsedAs returns the declared signature of expr when expr is a function
// literal, or a reference to a function declared in this package, whose
// signature is identical to the underlying signature of target, and target is
// a named function type declared in another module. It returns nil otherwise.
func funcTypeUsedAs(
	pass *analysis.Pass,
	declByFunc map[*types.Func]*ast.FuncDecl,
	expr ast.Expr,
	target types.Type,
) *ast.FuncType {
	want := externalNamedSignature(pass, target)
	if want == nil {
		return nil
	}
	expr = ast.Unparen(expr)
	if !types.Identical(pass.TypesInfo.TypeOf(expr), want) {
		return nil
	}
	var ident *ast.Ident
	switch typed := expr.(type) {
	case *ast.FuncLit:
		return typed.Type
	case *ast.Ident:
		ident = typed
	case *ast.SelectorExpr:
		ident = typed.Sel
	default:
		return nil
	}
	fn, ok := pass.TypesInfo.Uses[ident].(*types.Func)
	if !ok {
		return nil
	}
	if decl := declByFunc[fn.Origin()]; decl != nil {
		return decl.Type
	}
	return nil
}

// externalNamedSignature returns the underlying signature of t when t is a
// named function type declared in another module, and nil otherwise.
func externalNamedSignature(pass *analysis.Pass, t types.Type) *types.Signature {
	if t == nil {
		return nil
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || !objectIsExternal(pass, named.Obj()) {
		return nil
	}
	sig, ok := named.Underlying().(*types.Signature)
	if !ok {
		return nil
	}
	return sig
}

// forEachExpectedType calls visit for every expression in file that is used
// where a known type is expected: a return value against the enclosing
// function's result type, an assignment or typed declaration against its
// left-hand type, a call argument against its parameter type, a conversion
// operand against the conversion type, and a composite literal element against
// its field, element, or value type.
func forEachExpectedType(pass *analysis.Pass, file *ast.File, visit func(ast.Expr, types.Type)) {
	var stack []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		switch typed := node.(type) {
		case *ast.ReturnStmt:
			visitReturn(pass, stack, typed, visit)
		case *ast.AssignStmt:
			if len(typed.Lhs) == len(typed.Rhs) {
				for i, rhs := range typed.Rhs {
					visit(rhs, pass.TypesInfo.TypeOf(typed.Lhs[i]))
				}
			}
		case *ast.ValueSpec:
			if typed.Type != nil {
				target := pass.TypesInfo.TypeOf(typed.Type)
				for _, value := range typed.Values {
					visit(value, target)
				}
			}
		case *ast.CallExpr:
			visitCall(pass, typed, visit)
		case *ast.CompositeLit:
			visitCompositeLit(pass, typed, visit)
		}
		return true
	})
}

func visitReturn(
	pass *analysis.Pass,
	stack []ast.Node,
	ret *ast.ReturnStmt,
	visit func(ast.Expr, types.Type),
) {
	sig := enclosingSignature(pass, stack)
	if sig == nil || sig.Results().Len() != len(ret.Results) {
		return
	}
	for i, result := range ret.Results {
		visit(result, sig.Results().At(i).Type())
	}
}

// enclosingSignature returns the signature of the innermost function literal
// or function declaration on stack.
func enclosingSignature(pass *analysis.Pass, stack []ast.Node) *types.Signature {
	for _, node := range slices.Backward(stack) {
		var t types.Type
		switch typed := node.(type) {
		case *ast.FuncLit:
			t = pass.TypesInfo.TypeOf(typed)
		case *ast.FuncDecl:
			if obj := pass.TypesInfo.Defs[typed.Name]; obj != nil {
				t = obj.Type()
			}
		default:
			continue
		}
		sig, _ := t.(*types.Signature)
		return sig
	}
	return nil
}

func visitCall(pass *analysis.Pass, call *ast.CallExpr, visit func(ast.Expr, types.Type)) {
	funTV, ok := pass.TypesInfo.Types[call.Fun]
	if !ok || funTV.Type == nil {
		return
	}
	if funTV.IsType() {
		if len(call.Args) == 1 {
			visit(call.Args[0], funTV.Type)
		}
		return
	}
	sig, ok := funTV.Type.Underlying().(*types.Signature)
	if !ok {
		return
	}
	for i, arg := range call.Args {
		visit(arg, callParamType(sig, i, call.Ellipsis.IsValid()))
	}
}

// callParamType returns the type the i-th argument of a call to sig is
// assigned to. Arguments past the last fixed parameter of a variadic call are
// assigned to the slice element type unless the call spreads a slice with ...
func callParamType(sig *types.Signature, i int, spread bool) types.Type {
	params := sig.Params()
	last := params.Len() - 1
	if last < 0 {
		return nil
	}
	if !sig.Variadic() || i < last {
		if i > last {
			return nil
		}
		return params.At(i).Type()
	}
	if spread {
		return params.At(last).Type()
	}
	slice, ok := params.At(last).Type().Underlying().(*types.Slice)
	if !ok {
		return nil
	}
	return slice.Elem()
}

func visitCompositeLit(pass *analysis.Pass, lit *ast.CompositeLit, visit func(ast.Expr, types.Type)) {
	litType := pass.TypesInfo.TypeOf(lit)
	if litType == nil {
		return
	}
	switch underlying := litType.Underlying().(type) {
	case *types.Struct:
		visitStructLitFields(underlying, lit, visit)
	case *types.Slice:
		visitElements(lit, underlying.Elem(), visit)
	case *types.Array:
		visitElements(lit, underlying.Elem(), visit)
	case *types.Map:
		visitElements(lit, underlying.Elem(), visit)
	}
}

func visitStructLitFields(structType *types.Struct, lit *ast.CompositeLit, visit func(ast.Expr, types.Type)) {
	for i, elt := range lit.Elts {
		keyValue, keyed := elt.(*ast.KeyValueExpr)
		if !keyed {
			if i < structType.NumFields() {
				visit(elt, structType.Field(i).Type())
			}
			continue
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if !ok {
			continue
		}
		for field := range structType.Fields() {
			if field.Name() == key.Name {
				visit(keyValue.Value, field.Type())
			}
		}
	}
}

func visitElements(lit *ast.CompositeLit, elem types.Type, visit func(ast.Expr, types.Type)) {
	for _, elt := range lit.Elts {
		if keyValue, keyed := elt.(*ast.KeyValueExpr); keyed {
			elt = keyValue.Value
		}
		visit(elt, elem)
	}
}

// methodImplementsExternalInterface reports whether method's receiver type
// implements an interface declared in another module that the package imports
// directly, and that interface declares a method with method's name.
func methodImplementsExternalInterface(pass *analysis.Pass, method *types.Func) bool {
	for _, imported := range pass.Pkg.Imports() {
		scope := imported.Scope()
		for _, name := range scope.Names() {
			typeName, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || !objectIsExternal(pass, typeName) {
				continue
			}
			iface := methodSetInterface(typeName)
			if iface != nil && receiverImplementsInterfaceMethod(method, iface) {
				return true
			}
		}
	}
	return false
}

// methodSetInterface returns the interface typeName declares when it is a
// non-generic interface with at least one method, and nil otherwise.
func methodSetInterface(typeName *types.TypeName) *types.Interface {
	if named, ok := typeName.Type().(*types.Named); ok && named.TypeParams().Len() > 0 {
		return nil
	}
	iface, ok := typeName.Type().Underlying().(*types.Interface)
	if !ok || !iface.IsMethodSet() || iface.NumMethods() == 0 {
		return nil
	}
	return iface
}

// receiverImplementsInterfaceMethod reports whether method is one of iface's
// methods and its receiver type, or a pointer to it, implements iface.
func receiverImplementsInterfaceMethod(method *types.Func, iface *types.Interface) bool {
	sig, ok := method.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}
	declared := false
	for ifaceMethod := range iface.Methods() {
		if ifaceMethod.Name() == method.Name() {
			declared = true
			break
		}
	}
	if !declared {
		return false
	}
	base := sig.Recv().Type()
	if pointer, isPointer := base.(*types.Pointer); isPointer {
		base = pointer.Elem()
	}
	return types.Implements(base, iface) || types.Implements(types.NewPointer(base), iface)
}
