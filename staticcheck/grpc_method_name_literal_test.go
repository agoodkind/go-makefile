package staticcheck

import (
	"testing"
)

func TestGrpcMethodNameLiteralFlagsBareLiteralOnMethodCall(t *testing.T) {
	t.Parallel()

	source := `package clydesup

type Stream struct{}

type GRPCRelay struct{}

func (r *GRPCRelay) RelayStream(s *Stream, fullMethod string, payload []byte) error {
	return nil
}

func dispatch() error {
	r := &GRPCRelay{}
	var s *Stream
	return r.RelayStream(s, "SubscribeRegistry", nil)
}
`
	a := newGrpcMethodNameLiteralAnalyzer()
	if err := a.Flags.Set("targets", "example.com/sample.GRPCRelay.RelayStream:1"); err != nil {
		t.Fatalf("set targets: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "relay.go", source)
	wantOnce(t, diags, "[GRPCMETHOD001]", "SubscribeRegistry", "argument index 1")
}

func TestGrpcMethodNameLiteralFlagsBareLiteralOnFunctionCall(t *testing.T) {
	t.Parallel()

	source := `package routing

func RouteUnary(fullMethod string, payload []byte) error {
	return nil
}

func dispatch() error {
	return RouteUnary("Foo", nil)
}
`
	a := newGrpcMethodNameLiteralAnalyzer()
	if err := a.Flags.Set("targets", "example.com/sample.RouteUnary:0"); err != nil {
		t.Fatalf("set targets: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "route.go", source)
	wantOnce(t, diags, "[GRPCMETHOD001]", "Foo", "argument index 0")
}

func TestGrpcMethodNameLiteralAcceptsFullMethodNameConstant(t *testing.T) {
	t.Parallel()

	source := `package clydesup

const ClydeService_SubscribeRegistry_FullMethodName = "/clyde.v1.ClydeService/SubscribeRegistry"

type Stream struct{}

type GRPCRelay struct{}

func (r *GRPCRelay) RelayStream(s *Stream, fullMethod string, payload []byte) error {
	return nil
}

func dispatch() error {
	r := &GRPCRelay{}
	var s *Stream
	return r.RelayStream(s, ClydeService_SubscribeRegistry_FullMethodName, nil)
}
`
	a := newGrpcMethodNameLiteralAnalyzer()
	if err := a.Flags.Set("targets", "example.com/sample.GRPCRelay.RelayStream:1"); err != nil {
		t.Fatalf("set targets: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "relay.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for _FullMethodName constant, got %d: %v", len(diags), diags)
	}
}

func TestGrpcMethodNameLiteralAcceptsArbitraryConstant(t *testing.T) {
	t.Parallel()

	source := `package clydesup

const someOtherName = "/clyde.v1.ClydeService/SubscribeRegistry"

type Stream struct{}

type GRPCRelay struct{}

func (r *GRPCRelay) RelayStream(s *Stream, fullMethod string, payload []byte) error {
	return nil
}

func dispatch() error {
	r := &GRPCRelay{}
	var s *Stream
	return r.RelayStream(s, someOtherName, nil)
}
`
	a := newGrpcMethodNameLiteralAnalyzer()
	if err := a.Flags.Set("targets", "example.com/sample.GRPCRelay.RelayStream:1"); err != nil {
		t.Fatalf("set targets: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "relay.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for non-literal constant reference, got %d: %v", len(diags), diags)
	}
}

func TestGrpcMethodNameLiteralAcceptsNonStringExpression(t *testing.T) {
	t.Parallel()

	source := `package clydesup

type Stream struct{}

type GRPCRelay struct{}

func (r *GRPCRelay) RelayStream(s *Stream, fullMethod string, payload []byte) error {
	return nil
}

func methodNameFor(_ string) string { return "" }

func dispatch(prefix string) error {
	r := &GRPCRelay{}
	var s *Stream
	return r.RelayStream(s, methodNameFor(prefix), nil)
}
`
	a := newGrpcMethodNameLiteralAnalyzer()
	if err := a.Flags.Set("targets", "example.com/sample.GRPCRelay.RelayStream:1"); err != nil {
		t.Fatalf("set targets: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "relay.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for function-call expression, got %d: %v", len(diags), diags)
	}
}

func TestGrpcMethodNameLiteralAcceptsConcatenation(t *testing.T) {
	t.Parallel()

	source := `package clydesup

type Stream struct{}

type GRPCRelay struct{}

func (r *GRPCRelay) RelayStream(s *Stream, fullMethod string, payload []byte) error {
	return nil
}

func dispatch(prefix string) error {
	r := &GRPCRelay{}
	var s *Stream
	return r.RelayStream(s, prefix+"SubscribeRegistry", nil)
}
`
	a := newGrpcMethodNameLiteralAnalyzer()
	if err := a.Flags.Set("targets", "example.com/sample.GRPCRelay.RelayStream:1"); err != nil {
		t.Fatalf("set targets: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "relay.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for concatenation expression, got %d: %v", len(diags), diags)
	}
}

func TestGrpcMethodNameLiteralEmptyTargetsDoesNothing(t *testing.T) {
	t.Parallel()

	source := `package clydesup

type Stream struct{}

type GRPCRelay struct{}

func (r *GRPCRelay) RelayStream(s *Stream, fullMethod string, payload []byte) error {
	return nil
}

func dispatch() error {
	r := &GRPCRelay{}
	var s *Stream
	return r.RelayStream(s, "SubscribeRegistry", nil)
}
`
	a := newGrpcMethodNameLiteralAnalyzer()
	diags := runAnalyzerOnSource(t, a, "relay.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics with empty targets list, got %d: %v", len(diags), diags)
	}
}

func TestGrpcMethodNameLiteralIgnoresUnrelatedCalls(t *testing.T) {
	t.Parallel()

	source := `package clydesup

func Other(name string) error { return nil }

func dispatch() error {
	return Other("SubscribeRegistry")
}
`
	a := newGrpcMethodNameLiteralAnalyzer()
	if err := a.Flags.Set("targets", "example.com/sample.GRPCRelay.RelayStream:1"); err != nil {
		t.Fatalf("set targets: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "relay.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for unrelated call, got %d: %v", len(diags), diags)
	}
}
