package consumer

import "contracts"

func fixedFields() {
	_ = contracts.FixedFields{Callback: func(any) error { return nil }}
	_ = contracts.FixedFields{func(any) error { return nil }}
	var fixed contracts.FixedFields
	fixed.Callback = func(any) error { return nil }
	fixed.Callback = func(any) error {
		var local any // want "do not use any" "signature uses any"
		_ = local
		return nil
	}
	fixed.Callback = fieldHelper
}

func fieldHelper(any) error { return nil } // want "do not use any" "signature uses any"

func selectedFields() {
	_ = contracts.SelectedFields[any]{Callback: func(any) error { return nil }} // want "do not use any" "do not use any" "signature uses any"
	_ = contracts.SelectedFields[any]{func(any) error { return nil }}           // want "do not use any" "do not use any" "signature uses any"
	var selected contracts.SelectedFields[any]                                  // want "do not use any"
	selected.Callback = func(any) error { return nil }                          // want "do not use any" "signature uses any"
}

type localFields struct {
	Callback contracts.Callback[any] // want "do not use any"
}

func localFieldAssignments() {
	_ = localFields{Callback: func(any) error { return nil }} // want "do not use any" "signature uses any"
	var local localFields
	local.Callback = func(any) error { return nil } // want "do not use any" "signature uses any"
}
