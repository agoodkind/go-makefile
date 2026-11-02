package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/analysis"
)

// signatureBoundaries records compiler requirements for written signatures.
// It never exempts a function body or an independently declared helper.
type signatureBoundaries struct {
	pass     *analysis.Pass
	methods  map[*types.Func]*ast.FuncType
	required map[*ast.FuncType]*types.Signature
	origins  signatureOrigins
}

func externalSignatureRequirements(pass *analysis.Pass) map[*ast.FuncType]*types.Signature {
	boundaries := &signatureBoundaries{
		pass: pass, methods: make(map[*types.Func]*ast.FuncType),
		required: make(map[*ast.FuncType]*types.Signature),
	}
	for _, file := range pass.Files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil {
				continue
			}
			if method, ok := pass.TypesInfo.Defs[function.Name].(*types.Func); ok {
				boundaries.methods[method.Origin()] = function.Type
			}
		}
	}
	// Revisit return contexts after discovering additional required methods.
	for previousCount := -1; previousCount != len(boundaries.required); {
		previousCount = len(boundaries.required)
		for _, file := range pass.Files {
			boundaries.walk(file)
		}
	}
	return boundaries.required
}

func (boundaries *signatureBoundaries) walk(file *ast.File) {
	var stack []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		boundaries.visit(node, stack)
		stack = append(stack, node)
		return true
	})
}

func (boundaries *signatureBoundaries) visit(node ast.Node, stack []ast.Node) {
	switch current := node.(type) {
	case *ast.CallExpr:
		boundaries.call(current)
	case *ast.ValueSpec:
		if current.Type != nil {
			for _, value := range current.Values {
				boundaries.require(value, boundaries.pass.TypesInfo.TypeOf(current.Type))
			}
		}
	case *ast.AssignStmt:
		boundaries.assignment(current)
	case *ast.CompositeLit:
		boundaries.composite(current)
	case *ast.ReturnStmt:
		boundaries.returns(current, stack)
	}
}

func (boundaries *signatureBoundaries) assignment(statement *ast.AssignStmt) {
	if len(statement.Lhs) != len(statement.Rhs) {
		return
	}
	for index, value := range statement.Rhs {
		target := statement.Lhs[index]
		if name, ok := target.(*ast.Ident); ok && statement.Tok == token.DEFINE {
			if boundaries.pass.TypesInfo.Defs[name] != nil {
				continue
			}
		}
		boundaries.require(value, boundaries.pass.TypesInfo.TypeOf(target))
	}
}

func (boundaries *signatureBoundaries) call(call *ast.CallExpr) {
	info := boundaries.pass.TypesInfo
	if info.Types[call.Fun].IsType() {
		if len(call.Args) == 1 {
			boundaries.require(call.Args[0], info.TypeOf(call.Fun))
		}
		return
	}
	signature, ok := info.TypeOf(call.Fun).Underlying().(*types.Signature)
	if !ok {
		return
	}
	for index, argument := range call.Args {
		parameterIndex := index
		if signature.Variadic() && index >= signature.Params().Len()-1 {
			parameterIndex = signature.Params().Len() - 1
		}
		if parameterIndex >= signature.Params().Len() {
			break
		}
		expected := signature.Params().At(parameterIndex).Type()
		if signature.Variadic() && parameterIndex == signature.Params().Len()-1 && !call.Ellipsis.IsValid() {
			expected = requiredElement(expected)
		}
		boundaries.require(argument, expected)
	}
}

func (boundaries *signatureBoundaries) composite(literal *ast.CompositeLit) {
	target := boundaries.pass.TypesInfo.TypeOf(literal)
	if target == nil {
		return
	}
	for index, element := range literal.Elts {
		value := element
		pair, keyed := element.(*ast.KeyValueExpr)
		if keyed {
			value = pair.Value
		}
		switch shape := target.Underlying().(type) {
		case *types.Struct:
			field := compositeField(shape, index, pair)
			if field != nil {
				boundaries.require(value, field.Type())
			}
		case *types.Map:
			if keyed {
				boundaries.require(pair.Key, shape.Key())
			}
			boundaries.require(value, shape.Elem())
		case *types.Slice:
			boundaries.require(value, shape.Elem())
		case *types.Array:
			boundaries.require(value, shape.Elem())
		}
	}
}

func compositeField(shape *types.Struct, index int, pair *ast.KeyValueExpr) *types.Var {
	if pair == nil {
		if index < shape.NumFields() {
			return shape.Field(index)
		}
		return nil
	}
	name, ok := pair.Key.(*ast.Ident)
	if !ok {
		return nil
	}
	for field := range shape.Fields() {
		if field.Name() == name.Name {
			return field
		}
	}
	return nil
}

func (boundaries *signatureBoundaries) returns(statement *ast.ReturnStmt, stack []ast.Node) {
	for _, ancestor := range slices.Backward(stack) {
		var function *ast.FuncType
		var actual types.Type
		switch parent := ancestor.(type) {
		case *ast.FuncDecl:
			function = parent.Type
			actual = boundaries.pass.TypesInfo.Defs[parent.Name].Type()
		case *ast.FuncLit:
			function = parent.Type
			actual = boundaries.pass.TypesInfo.TypeOf(parent)
		default:
			continue
		}
		signature := boundaries.required[function]
		if signature == nil {
			signature, _ = actual.(*types.Signature)
		}
		actualSignature, _ := actual.(*types.Signature)
		if signature != nil && actualSignature != nil && signature.Results().Len() == len(statement.Results) {
			for resultIndex, result := range statement.Results {
				boundaries.requirePattern(result, actualSignature.Results().At(resultIndex).Type(), signature.Results().At(resultIndex).Type())
			}
		}
		return
	}
}

func (boundaries *signatureBoundaries) require(expression ast.Expr, expected types.Type) {
	boundaries.requirePattern(expression, expected, expected)
}

func (boundaries *signatureBoundaries) requirePattern(expression ast.Expr, expected, pattern types.Type) {
	if expected == nil || pattern == nil {
		return
	}
	actual := boundaries.pass.TypesInfo.TypeOf(expression)
	if actual == nil || !types.AssignableTo(actual, expected) {
		return
	}
	if literal, ok := ast.Unparen(expression).(*ast.FuncLit); ok {
		if signature, ok := originalType(pattern).Underlying().(*types.Signature); ok && boundaries.externalFunction(pattern) {
			boundaries.required[literal.Type] = boundaries.origins.original(signature)
		}
	}
	if contract, ok := expected.Underlying().(*types.Interface); ok {
		boundaries.implementation(actual, contract)
	}
}

func originalType(target types.Type) types.Type {
	if named, ok := types.Unalias(target).(*types.Named); ok {
		return named.Origin()
	}
	return target
}

func (boundaries *signatureBoundaries) externalFunction(target types.Type) bool {
	if definition := typeDefiningObject(target); definition != nil {
		return objectIsExternal(boundaries.pass, definition)
	}
	signature, ok := target.(*types.Signature)
	if !ok {
		return false
	}
	for _, tuple := range []*types.Tuple{signature.Params(), signature.Results()} {
		for variable := range tuple.Variables() {
			if objectIsExternal(boundaries.pass, variable) {
				return true
			}
		}
	}
	return false
}

func (boundaries *signatureBoundaries) implementation(actual types.Type, contract *types.Interface) {
	if !types.Implements(actual, contract) {
		return
	}
	for requiredMethod := range contract.Methods() {
		if !objectIsExternal(boundaries.pass, requiredMethod) {
			continue
		}
		object, _, _ := types.LookupFieldOrMethod(actual, false, requiredMethod.Pkg(), requiredMethod.Name())
		method, ok := object.(*types.Func)
		if !ok || !types.Identical(method.Type(), requiredMethod.Type()) {
			continue
		}
		if declaration := boundaries.methods[method.Origin()]; declaration != nil {
			if signature, ok := requiredMethod.Origin().Type().(*types.Signature); ok {
				boundaries.required[declaration] = signature
			}
		}
	}
}
