package consumer

import "contracts"

func genericCallbacks() {
	contracts.Generic(func(inferred any, fixed any) any { // want "do not use any" "signature uses any" "do not use any" "signature uses any"
		return inferred
	})
	contracts.Generic(func(inferred string, fixed any) string {
		return inferred
	})
	contracts.Nested(func(values map[any]any, callback func(any, any)) any { // want "do not use any" "signature uses any" "do not use any" "signature uses any" "do not use any" "signature uses any"
		return nil
	})
	function := contracts.Generic[any]
	function(func(inferred any, fixed any) any { return inferred }) // want "do not use any" "signature uses any" "do not use any" "signature uses any"
}

var genericHandler contracts.GenericHandler[any] = func(fixed, inferred any) {} // want "do not use any" "do not use any" "signature uses any"

type genericCodec struct{}

func (*genericCodec) Encode(fixed any, inferred any) error { return nil } // want "do not use any" "signature uses any"

var _ contracts.GenericCodec[any] = (*genericCodec)(nil) // want "do not use any"
