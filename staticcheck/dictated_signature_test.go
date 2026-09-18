package staticcheck

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
)

// rpcStubPath is the import path of a stand-in for a gRPC-style dependency.
// The fixture writes it under a pkg/mod directory so objectIsExternal treats
// it as declared in another module, as it would a real module-cache package.
const rpcStubPath = "example.com/rpc"

const rpcStubSource = `package rpc

import "context"

type ServerInfo struct{ FullMethod string }

type Handler func(ctx context.Context, req any) (any, error)

type UnaryServerInterceptor func(ctx context.Context, req any, info *ServerInfo, handler Handler) (resp any, err error)

func ChainUnary(interceptors ...UnaryServerInterceptor) UnaryServerInterceptor { return nil }

type Codec interface {
	Marshal(v any) ([]byte, error)
	Name() string
}
`

// TestNoAnyExemptsLiteralReturnedAsExternalFuncType mirrors a gRPC unary
// interceptor factory: the literal's `any` parameters and results are fixed by
// the external named function type it is returned as.
func TestNoAnyExemptsLiteralReturnedAsExternalFuncType(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import (
	"context"

	"example.com/rpc"
)

func traceInterceptor() rpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *rpc.ServerInfo,
		handler rpc.Handler,
	) (any, error) {
		return handler(ctx, req)
	}
}
`
	diags := runAnalyzerWithRPCStub(t, NoAnyOrEmptyInterfaceAnalyzer, source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for a literal returned as rpc.UnaryServerInterceptor, got %d: %v", len(diags), diags)
	}
}

// TestNoAnyExemptsDeclaredFuncPassedAsExternalFuncType covers a declared
// function whose only use is as an argument of the external named type.
func TestNoAnyExemptsDeclaredFuncPassedAsExternalFuncType(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import (
	"context"

	"example.com/rpc"
)

func logInterceptor(ctx context.Context, req any, info *rpc.ServerInfo, handler rpc.Handler) (any, error) {
	return handler(ctx, req)
}

var chain = rpc.ChainUnary(logInterceptor)
`
	diags := runAnalyzerWithRPCStub(t, NoAnyOrEmptyInterfaceAnalyzer, source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for a function passed as rpc.UnaryServerInterceptor, got %d: %v", len(diags), diags)
	}
}

// TestNoAnyExemptsExternalInterfaceMethod covers a method whose receiver type
// implements an interface declared in another module.
func TestNoAnyExemptsExternalInterfaceMethod(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import "example.com/rpc"

type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error) { return nil, nil }

func (jsonCodec) Name() string { return "json" }

var _ rpc.Codec = jsonCodec{}
`
	diags := runAnalyzerWithRPCStub(t, NoAnyOrEmptyInterfaceAnalyzer, source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for a method implementing rpc.Codec, got %d: %v", len(diags), diags)
	}
}

// TestNoAnyFlagsMatchingSignatureNeverUsedAsExternalType verifies that sharing
// the external signature is not enough: the function must be used as the type.
func TestNoAnyFlagsMatchingSignatureNeverUsedAsExternalType(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import (
	"context"

	"example.com/rpc"
)

func lookalike(ctx context.Context, req any, info *rpc.ServerInfo, handler rpc.Handler) (any, error) {
	return handler(ctx, req)
}
`
	diags := runAnalyzerWithRPCStub(t, NoAnyOrEmptyInterfaceAnalyzer, source)
	wantDiagnostic(t, diags, "do not use any")
}

// TestNoAnyFlagsMethodWhoseTypeDoesNotImplementExternalInterface verifies that
// a method sharing an interface method's name and signature still fires when
// its receiver type does not implement the interface.
func TestNoAnyFlagsMethodWhoseTypeDoesNotImplementExternalInterface(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import "example.com/rpc"

type partialCodec struct{}

func (partialCodec) Marshal(v any) ([]byte, error) { return nil, nil }

var _ = rpc.ChainUnary
`
	diags := runAnalyzerWithRPCStub(t, NoAnyOrEmptyInterfaceAnalyzer, source)
	wantDiagnostic(t, diags, "do not use any")
}

// TestNoAnyFlagsAnyInsideDictatedFuncBody verifies that the exemption covers
// only the dictated signature, not the body of the function.
func TestNoAnyFlagsAnyInsideDictatedFuncBody(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import (
	"context"

	"example.com/rpc"
)

func traceInterceptor() rpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *rpc.ServerInfo, handler rpc.Handler) (any, error) {
		var scratch any = req
		return handler(ctx, scratch)
	}
}
`
	diags := runAnalyzerWithRPCStub(t, NoAnyOrEmptyInterfaceAnalyzer, source)
	wantDiagnostic(t, diags, "do not use any")
	// The literal scan and the types scan both report the body's `any` at one
	// position. A report on the signature would carry a different position.
	for _, d := range diags {
		if d.Pos != diags[0].Pos {
			t.Fatalf("expected every diagnostic at the body's any, got %d positions: %v", len(diags), diags)
		}
	}
}

// TestWrappedErrorExemptsSlogHandlerMethod mirrors a slog.Handler wrapper whose
// Handle method wraps the next handler's error. Logging there would re-enter
// the handler.
func TestWrappedErrorExemptsSlogHandlerMethod(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import (
	"context"
	"fmt"
	"log/slog"
)

type contextHandler struct{ next slog.Handler }

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.next.Handle(ctx, record); err != nil {
		return fmt.Errorf("next handler: %w", err)
	}
	return nil
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}
`
	diags := runAnalyzerWithRPCStub(t, WrappedErrorWithoutSlogAnalyzer, source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for a slog.Handler Handle method, got %d: %v", len(diags), diags)
	}
}

// TestWrappedErrorFlagsHandleOnNonHandlerType verifies that a Handle method on
// a type that does not implement slog.Handler still fires.
func TestWrappedErrorFlagsHandleOnNonHandlerType(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import (
	"context"
	"fmt"
	"log/slog"
)

type recordSink struct{ next slog.Handler }

func (s *recordSink) Handle(ctx context.Context, record slog.Record) error {
	if err := s.next.Handle(ctx, record); err != nil {
		return fmt.Errorf("sink: %w", err)
	}
	return nil
}
`
	diags := runAnalyzerWithRPCStub(t, WrappedErrorWithoutSlogAnalyzer, source)
	wantDiagnostic(t, diags, "function Handle returns a wrapped error")
}

// TestWrappedErrorFlagsNonInterfaceMethodOnSlogHandler verifies that a helper
// method outside the slog.Handler interface still fires, even on a type that
// implements it.
func TestWrappedErrorFlagsNonInterfaceMethodOnSlogHandler(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import (
	"context"
	"fmt"
	"log/slog"
)

type contextHandler struct{ next slog.Handler }

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool { return true }

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error { return nil }

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }

func (h *contextHandler) WithGroup(name string) slog.Handler { return h }

func (h *contextHandler) Flush(ctx context.Context, record slog.Record) error {
	if err := h.next.Handle(ctx, record); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}
`
	diags := runAnalyzerWithRPCStub(t, WrappedErrorWithoutSlogAnalyzer, source)
	wantDiagnostic(t, diags, "function Flush returns a wrapped error")
}

// stubImporter returns the stand-in package for its path and defers every
// other import to one shared source importer, so the stub and the consumer see
// the same standard-library type objects.
type stubImporter struct {
	path     string
	pkg      *types.Package
	fallback types.Importer
}

func (s stubImporter) Import(path string) (*types.Package, error) {
	if path == s.path {
		return s.pkg, nil
	}
	return s.fallback.Import(path)
}

// runAnalyzerWithRPCStub type-checks the rpc stand-in from a pkg/mod path and
// source against it, then runs analyzer over source. The consumer file sits in
// a temp dir so the analyzer's self-skip for "/staticcheck/" paths does not
// apply.
func runAnalyzerWithRPCStub(t *testing.T, analyzer *analysis.Analyzer, source string) []analysis.Diagnostic {
	t.Helper()

	dir := t.TempDir()
	fset := token.NewFileSet()
	fallback := importer.ForCompiler(fset, "source", nil)

	stubDir := filepath.Join(dir, "pkg", "mod", "example.com", "rpc@v1.0.0")
	if err := os.MkdirAll(stubDir, 0o750); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	stubFile, err := parser.ParseFile(fset, filepath.Join(stubDir, "rpc.go"), rpcStubSource, 0)
	if err != nil {
		t.Fatalf("parser.ParseFile stub: %v", err)
	}
	stubConf := types.Config{Importer: fallback}
	stub, err := stubConf.Check(rpcStubPath, fset, []*ast.File{stubFile}, nil)
	if err != nil {
		t.Fatalf("types.Config.Check stub: %v", err)
	}

	file, err := parser.ParseFile(fset, filepath.Join(dir, "consumer.go"), source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parser.ParseFile: %v", err)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: stubImporter{path: rpcStubPath, pkg: stub, fallback: fallback}}
	pkg, err := conf.Check("example.com/app/consumerpkg", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatalf("types.Config.Check: %v", err)
	}

	var diags []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer:  analyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
		Report:    func(d analysis.Diagnostic) { diags = append(diags, d) },
	}
	if _, err := analyzer.Run(pass); err != nil {
		t.Fatalf("%s.Run: %v", analyzer.Name, err)
	}
	return diags
}
