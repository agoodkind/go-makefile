package staticcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// checkRequiredFunction checks positions that an external contract leaves
// generic. Only types fixed by that contract qualify for the exemption.
func checkRequiredFunction(pass *analysis.Pass, file *ast.File, function *ast.FuncType, contracts []*types.Signature) {
	var parameters, results []*types.Tuple
	for _, contract := range contracts {
		parameters = append(parameters, contract.Params())
		results = append(results, contract.Results())
	}
	checkRequiredFields(pass, file, function.Params, parameters)
	checkRequiredFields(pass, file, function.Results, results)
}

func checkRequiredFields(pass *analysis.Pass, file *ast.File, fields *ast.FieldList, contracts []*types.Tuple) {
	if fields == nil {
		return
	}
	index := 0
	for _, field := range fields.List {
		for range max(1, len(field.Names)) {
			var required []types.Type
			for _, contract := range contracts {
				required = append(required, contract.At(index).Type())
			}
			checkRequiredType(pass, file, field.Type, required)
			index++
		}
	}
}

func checkRequiredType(pass *analysis.Pass, file *ast.File, expression ast.Expr, contracts []types.Type) {
	for _, contract := range contracts {
		if types.Identical(pass.TypesInfo.TypeOf(expression), contract) {
			return
		}
	}
	if function, ok := ast.Unparen(expression).(*ast.FuncType); ok {
		var signatures []*types.Signature
		for _, contract := range contracts {
			if signature, ok := contract.Underlying().(*types.Signature); ok {
				signatures = append(signatures, signature)
			}
		}
		checkRequiredFunction(pass, file, function, signatures)
		return
	}
	children := requiredChildren(expression, contracts)
	if len(children) != 0 {
		for _, child := range children {
			checkRequiredType(pass, file, child.expression, child.contracts)
		}
		return
	}
	checkSignatureExpr(pass, file, expression)
}

type requiredChild struct {
	expression ast.Expr
	contracts  []types.Type
}

// requiredChildren combines fixed positions from every matching contract.
func requiredChildren(expression ast.Expr, contracts []types.Type) []requiredChild {
	var children []requiredChild
	switch written := ast.Unparen(expression).(type) {
	case *ast.MapType:
		children = []requiredChild{{expression: written.Key}, {expression: written.Value}}
		for _, contract := range contracts {
			if shape, ok := contract.Underlying().(*types.Map); ok {
				children[0].contracts = append(children[0].contracts, shape.Key())
				children[1].contracts = append(children[1].contracts, shape.Elem())
			}
		}
	case *ast.ArrayType:
		children = []requiredChild{{expression: written.Elt}}
	case *ast.Ellipsis:
		children = []requiredChild{{expression: written.Elt}}
	case *ast.StarExpr:
		children = []requiredChild{{expression: written.X}}
	case *ast.ChanType:
		children = []requiredChild{{expression: written.Value}}
	}
	if len(children) == 1 {
		for _, contract := range contracts {
			if element := requiredElement(contract); element != nil {
				children[0].contracts = append(children[0].contracts, element)
			}
		}
	}
	return children
}

func requiredElement(contract types.Type) types.Type {
	switch shape := contract.Underlying().(type) {
	case *types.Slice:
		return shape.Elem()
	case *types.Array:
		return shape.Elem()
	case *types.Pointer:
		return shape.Elem()
	case *types.Chan:
		return shape.Elem()
	}
	return nil
}
