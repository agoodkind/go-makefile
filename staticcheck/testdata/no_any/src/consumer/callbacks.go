package consumer

import (
	"context"
	"contracts"
	"database/sql"
	"database/sql/driver"
	"local"
	"sync"
)

func callbacks(connection *sql.Conn, mapping *sync.Map) {
	connection.Raw(func(any) error { return driver.ErrBadConn })
	mapping.Range(func(interface{}, any) bool { return false })
	connection.Raw(func(any) error {
		var value any // want "do not use any" "signature uses any"
		_ = value
		return nil
	})
	connection.Raw(extracted)
	callback := func(any) error { return nil } // want "do not use any" "signature uses any"
	connection.Raw(callback)
	local.Callback(func(any) error { return nil })    // want "do not use any" "signature uses any"
	_ = local.Handler(func(any) error { return nil }) // want "do not use any" "signature uses any"
	_ = sync.OnceValue(func() any { return nil })     // want "do not use any" "signature uses any"
}

func extracted(any) error { return nil } // want "do not use any" "signature uses any"

var assigned contracts.Handler = func(any, context.Context, func(any) error) (any, error) { return nil, nil }

func takeHandler(contracts.Handler) {}

func handlers() {
	assigned = func(any, context.Context, func(any) error) (any, error) { return nil, nil }
	takeHandler(func(any, context.Context, func(any) error) (any, error) { return nil, nil })
	_ = contracts.Handler(func(any, context.Context, func(any) error) (any, error) { return nil, nil })
	_ = contracts.Descriptor{
		Handler: func(any, context.Context, func(any) error) (any, error) { return nil, nil },
		Inline:  func(any) error { return nil },
	}
	_ = contracts.Descriptor{
		func(any, context.Context, func(any) error) (any, error) { return nil, nil },
		func(any) error { return nil },
	}
	_ = []contracts.Handler{func(any, context.Context, func(any) error) (any, error) { return nil, nil }}
	_ = map[string]contracts.Handler{"handler": func(any, context.Context, func(any) error) (any, error) { return nil, nil }}
}

func returnsHandler() contracts.Handler {
	return func(any, context.Context, func(any) error) (any, error) { return nil, nil }
}

var factory contracts.Factory = func() func(any) any {
	return func(any) any {
		var value interface{} // want "do not use interface" "signature uses interface"
		return value
	}
}

type LocalAlias = any               // want "do not use any" "type LocalAlias expands to any"
type LocalField struct{ Value any } // want "do not use any" "struct field type any"

var localFunction = func(any) any { return nil } // want "do not use any" "signature uses any" "do not use any" "signature uses any"
