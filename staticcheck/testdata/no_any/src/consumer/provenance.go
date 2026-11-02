package consumer

import (
	"contracts"
	"database/sql"
)

type fixedFirst struct{}

func (*fixedFirst) Scan(any) error { return nil }

type fixedLast struct{}

func (*fixedLast) Scan(any) error { return nil }

func acceptScanner(sql.Scanner) {}

func contractOrder() {
	acceptScanner(new(fixedFirst))
	contracts.AcceptGeneric[any](new(fixedFirst))
	contracts.AcceptGeneric[any](new(fixedLast))
	acceptScanner(new(fixedLast))
}

type fixedScanner struct{}

func (*fixedScanner) Scan(any) error {
	var local any // want "do not use any" "signature uses any"
	_ = local
	return nil
}

type callerScanner struct{}

func (*callerScanner) Scan(any) error { return nil } // want "do not use any" "signature uses any"

func fixedArguments() {
	contracts.AcceptFixed(new(fixedScanner))
	contracts.HandleFixed(func(any) error {
		var local any // want "do not use any" "signature uses any"
		_ = local
		return nil
	})
	contracts.AcceptGeneric[any](new(callerScanner))
	contracts.HandleGeneric(func(any) error { return nil }) // want "do not use any" "signature uses any"
	callback := contracts.HandleGeneric[any]
	callback(func(any) error { return nil }) // want "do not use any" "signature uses any"
	fixed := contracts.HandleFixed
	fixed(func(any) error { return nil })
}

type selectedScanner interface {
	contracts.Scanner[any] // want "do not use any"
}

type embeddedCaller struct{}

func (*embeddedCaller) Scan(any) error { return nil } // want "do not use any" "signature uses any"

var _ selectedScanner = new(embeddedCaller)

type embeddedFixed interface{ contracts.FixedScanner }

type fixedEmbeddedScanner struct{}

func (*fixedEmbeddedScanner) Scan(any) error { return nil }

var _ embeddedFixed = new(fixedEmbeddedScanner)

type mixedFirst struct{}

func (*mixedFirst) Build() func(map[any]any, func(any, any)) {
	return func(map[any]any, func(any, any)) {}
}

type mixedLast struct{}

func (*mixedLast) Build() func(map[any]any, func(any, any)) {
	return func(map[any]any, func(any, any)) {}
}

type mixedCaller struct{}

func (*mixedCaller) Build() func(map[any]any, func(any, any)) { // want "do not use any" "signature uses any" "do not use any" "signature uses any"
	return func(map[any]any, func(any, any)) {} // want "do not use any" "signature uses any" "do not use any" "signature uses any"
}

func mixedContracts() {
	contracts.AcceptFirst[any](new(mixedFirst))
	contracts.AcceptSecond[any](new(mixedFirst))
	contracts.AcceptSecond[any](new(mixedLast))
	contracts.AcceptFirst[any](new(mixedLast))
	contracts.AcceptFirst[any](new(mixedCaller))
}

type mixedEmbedded interface {
	contracts.First[any]  // want "do not use any"
	contracts.Second[any] // want "do not use any"
}

type embeddedBuilder struct{}

func (*embeddedBuilder) Build() func(map[any]any, func(any, any)) {
	return func(map[any]any, func(any, any)) {}
}

var _ mixedEmbedded = new(embeddedBuilder)
