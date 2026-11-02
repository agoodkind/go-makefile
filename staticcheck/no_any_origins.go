package staticcheck

import (
	"go/token"
	"go/types"
)

// signatureOrigins recovers declared callback types before generic
// instantiation substitutes types chosen by the caller.
type signatureOrigins struct {
	packages   map[*types.Package]bool
	signatures map[token.Pos]*types.Signature
}

func (origins *signatureOrigins) original(signature *types.Signature) *types.Signature {
	variable := signatureSource(signature)
	if variable == nil || variable.Pkg() == nil {
		return signature
	}
	if origins.packages == nil {
		origins.packages = make(map[*types.Package]bool)
		origins.signatures = make(map[token.Pos]*types.Signature)
	}
	owner := variable.Pkg()
	if !origins.packages[owner] {
		origins.packages[owner] = true
		visited := make(map[types.Type]bool)
		for _, name := range owner.Scope().Names() {
			object := owner.Scope().Lookup(name)
			switch object.(type) {
			case *types.Func, *types.TypeName:
				origins.collect(object.Type(), owner, visited)
			}
		}
	}
	if original := origins.signatures[variable.Pos()]; original != nil {
		return original
	}
	return signature
}

func signatureSource(signature *types.Signature) *types.Var {
	for _, tuple := range []*types.Tuple{signature.Params(), signature.Results()} {
		if tuple.Len() != 0 {
			return tuple.At(0)
		}
	}
	return nil
}

func (origins *signatureOrigins) collect(target types.Type, owner *types.Package, visited map[types.Type]bool) {
	if visited[target] {
		return
	}
	visited[target] = true
	switch shape := target.(type) {
	case *types.Named:
		if shape.Obj().Pkg() == owner {
			origins.collect(shape.Origin().Underlying(), owner, visited)
			for method := range shape.Origin().Methods() {
				origins.collect(method.Type(), owner, visited)
			}
		}
	case *types.Alias:
		origins.collect(shape.Origin().Rhs(), owner, visited)
	case *types.Signature:
		if variable := signatureSource(shape); variable != nil && variable.Pkg() == owner {
			origins.signatures[variable.Pos()] = shape
		}
		origins.collectTuple(shape.Params(), owner, visited)
		origins.collectTuple(shape.Results(), owner, visited)
	case *types.Interface:
		for method := range shape.Methods() {
			origins.collect(method.Origin().Type(), owner, visited)
		}
	case *types.Struct:
		for field := range shape.Fields() {
			origins.collect(field.Type(), owner, visited)
		}
	case *types.Map:
		origins.collect(shape.Key(), owner, visited)
		origins.collect(shape.Elem(), owner, visited)
	case *types.Slice:
		origins.collect(shape.Elem(), owner, visited)
	case *types.Array:
		origins.collect(shape.Elem(), owner, visited)
	case *types.Pointer:
		origins.collect(shape.Elem(), owner, visited)
	case *types.Chan:
		origins.collect(shape.Elem(), owner, visited)
	}
}

func (origins *signatureOrigins) collectTuple(tuple *types.Tuple, owner *types.Package, visited map[types.Type]bool) {
	for variable := range tuple.Variables() {
		origins.collect(variable.Type(), owner, visited)
	}
}
