package consumer

import (
	"contracts"
	"database/sql"
	"local"
)

type scanner struct{}

func (*scanner) Scan(any) error {
	var value any // want "do not use any" "signature uses any"
	_ = value
	return nil
}

var _ sql.Scanner = (*scanner)(nil)

func (*scanner) Extra(any) {} // want "do not use any" "signature uses any"

type lookalike struct{}

func (*lookalike) Scan(any) error { return nil } // want "do not use any" "signature uses any"

type converted struct{}

func (*converted) Scan(any) error { return nil }

var _ = sql.Scanner((*converted)(nil))

type parameter struct{}

func (*parameter) Scan(any) error { return nil }

func accept(sql.Scanner) {}

func useScanner() { accept(new(parameter)) }

type codec struct{}

func (*codec) Marshal(any) ([]byte, error) { return nil, nil }
func (*codec) Unmarshal([]byte, any) error { return nil }
func (*codec) Name() string                { return "codec" }

func register() { contracts.Register(new(codec)) }

type localScanner struct{}

func (*localScanner) Scan(any) error { return nil } // want "do not use any" "signature uses any"

func registerLocal() { local.Register(new(localScanner)) }
