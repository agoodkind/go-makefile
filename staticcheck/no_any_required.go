package staticcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// checkRequiredFunction checks positions that an external contract leaves
// generic. Only types fixed by that contract qualify for the exemption.
func checkRequiredFunction(pass *analysis.Pass, file *ast.File, function *ast.FuncType, contract *types.Signature) {
	checkRequiredFields(pass, file, function.Params, contract.Params())
	checkRequiredFields(pass, file, function.Results, contract.Results())
}

func checkRequiredFields(pass *analysis.Pass, file *ast.File, fields *ast.FieldList, contract *types.Tuple) {
	if fields == nil {
		return
	}
	index := 0
	for _, field := range fields.List {
		for range max(1, len(field.Names)) {
			checkRequiredType(pass, file, field.Type, contract.At(index).Type())
			index++
		}
	}
}

func checkRequiredType(pass *analysis.Pass, file *ast.File, expression ast.Expr, contract types.Type) {
	if types.Identical(pass.TypesInfo.TypeOf(expression), contract) {
		return
	}
	switch written := ast.Unparen(expression).(type) {
	case *ast.FuncType:
		if signature, ok := contract.Underlying().(*types.Signature); ok {
			checkRequiredFunction(pass, file, written, signature)
			return
		}
	case *ast.MapType:
		if shape, ok := contract.Underlying().(*types.Map); ok {
			checkRequiredType(pass, file, written.Key, shape.Key())
			checkRequiredType(pass, file, written.Value, shape.Elem())
			return
		}
	case *ast.ArrayType:
		if element := requiredElement(contract); element != nil {
			checkRequiredType(pass, file, written.Elt, element)
			return
		}
	case *ast.Ellipsis:
		if element := requiredElement(contract); element != nil {
			checkRequiredType(pass, file, written.Elt, element)
			return
		}
	case *ast.StarExpr:
		if shape, ok := contract.Underlying().(*types.Pointer); ok {
			checkRequiredType(pass, file, written.X, shape.Elem())
			return
		}
	case *ast.ChanType:
		if shape, ok := contract.Underlying().(*types.Chan); ok {
			checkRequiredType(pass, file, written.Value, shape.Elem())
			return
		}
	}
	checkSignatureExpr(pass, file, expression)
}

func requiredElement(contract types.Type) types.Type {
	switch shape := contract.Underlying().(type) {
	case *types.Slice:
		return shape.Elem()
	case *types.Array:
		return shape.Elem()
	}
	return nil
}
