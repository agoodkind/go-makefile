package staticcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

func TestRTAThrowawayRegistrationFlagsBypass(t *testing.T) {
	t.Parallel()

	source := `package supervisor

type fakeServer struct{}

func NewServer() *fakeServer { return &fakeServer{} }

func RegisterClydeServiceServer(s *fakeServer, impl interface{}) {}

type relay struct{}

func newSupervisorRelayComponents() *relay {
	r := &relay{}
	grpcServer := NewServer()
	RegisterClydeServiceServer(grpcServer, r)
	return r
}
`
	diags := runAnalyzerOnSource(t, RTAThrowawayRegistrationAnalyzer, "supervisor.go", source)
	wantOnce(t, diags, "[RTA003]", "grpcServer")
}

func TestRTAThrowawayRegistrationAcceptsServedServer(t *testing.T) {
	t.Parallel()

	source := `package supervisor

type fakeServer struct{}

func NewServer() *fakeServer { return &fakeServer{} }

func (s *fakeServer) Serve() error { return nil }

func RegisterClydeServiceServer(s *fakeServer, impl interface{}) {}

type relay struct{}

func runRealServer() error {
	r := &relay{}
	grpcServer := NewServer()
	RegisterClydeServiceServer(grpcServer, r)
	return grpcServer.Serve()
}
`
	diags := runAnalyzerOnSource(t, RTAThrowawayRegistrationAnalyzer, "supervisor.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for served server, got %d: %v", len(diags), diags)
	}
}

func TestRTAThrowawayRegistrationAcceptsReturnedServer(t *testing.T) {
	t.Parallel()

	source := `package supervisor

type fakeServer struct{}

func NewServer() *fakeServer { return &fakeServer{} }

func RegisterClydeServiceServer(s *fakeServer, impl interface{}) {}

type relay struct{}

func buildServer(r *relay) *fakeServer {
	grpcServer := NewServer()
	RegisterClydeServiceServer(grpcServer, r)
	return grpcServer
}
`
	diags := runAnalyzerOnSource(t, RTAThrowawayRegistrationAnalyzer, "supervisor.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics when server is returned, got %d: %v", len(diags), diags)
	}
}

// runAnalyzerOnSource parses a single file and runs analyzer against
// it inside a fresh *analysis.Pass that carries TypesInfo where
// resolvable. Helper kept inside the test file so tests stay self-
// contained without expanding the public API.
func runAnalyzerOnSource(t *testing.T, analyzer *analysis.Analyzer, filename string, source string) []analysis.Diagnostic {
	t.Helper()
	fset := token.NewFileSet()
	dir := t.TempDir()
	path := filepath.Join(dir, filename)
	file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parser.ParseFile: %v", err)
	}
	conf := types.Config{Error: func(error) {}}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	pkg, _ := conf.Check("example.com/sample", fset, []*ast.File{file}, info)
	if pkg == nil {
		pkg = types.NewPackage("example.com/sample", strings.TrimSuffix(filename, ".go"))
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
		t.Fatalf("analyzer.Run: %v", err)
	}
	return diags
}

func wantOnce(t *testing.T, diags []analysis.Diagnostic, mustContain ...string) {
	t.Helper()
	if len(diags) != 1 {
		t.Fatalf("expected exactly 1 diagnostic, got %d: %v", len(diags), diags)
	}
	for _, needle := range mustContain {
		if !strings.Contains(diags[0].Message, needle) {
			t.Fatalf("diagnostic %q missing substring %q", diags[0].Message, needle)
		}
	}
}
