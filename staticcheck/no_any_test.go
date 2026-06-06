package staticcheck

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

// TestNoAnyExemptsExternalNamedType verifies the module-boundary carve-out: a
// named type declared in the standard library (database/sql/driver.Value, whose
// underlying is `any`) is not flagged, because the consumer only named a type
// it did not author.
func TestNoAnyExemptsExternalNamedType(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

import "database/sql/driver"

func UsesExternal(v driver.Value) {}
`
	diags := runNoAnyAnalyzerOnSource(t, "example.com/app/consumerpkg", "consumer.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for external named type, got %d: %v", len(diags), diags)
	}
}

// TestNoAnyFlagsInRepoNamedType verifies that an in-repo named type that expands
// to an empty interface is still flagged when referenced by name in a signature.
func TestNoAnyFlagsInRepoNamedType(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

type LocalAny interface{}

func UsesLocal(v LocalAny) {}
`
	diags := runNoAnyAnalyzerOnSource(t, "example.com/app/consumerpkg", "consumer.go", source)
	wantDiagnostic(t, diags, "signature uses LocalAny")
	wantDiagnostic(t, diags, "type LocalAny expands to any")
}

// TestNoAnyFlagsLiteralAny verifies the literal-scan path is unaffected by the
// carve-out: a bare `any` parameter is still flagged.
func TestNoAnyFlagsLiteralAny(t *testing.T) {
	t.Parallel()

	source := `package consumerpkg

func UsesAny(v any) {}
`
	diags := runNoAnyAnalyzerOnSource(t, "example.com/app/consumerpkg", "consumer.go", source)
	wantDiagnostic(t, diags, "do not use any")
}

func wantDiagnostic(t *testing.T, diags []analysis.Diagnostic, substr string) {
	t.Helper()
	for _, d := range diags {
		if strings.Contains(d.Message, substr) {
			return
		}
	}
	t.Fatalf("expected a diagnostic containing %q, got %d: %v", substr, len(diags), diags)
}

// runNoAnyAnalyzerOnSource type-checks source with the standard-library source
// importer (so imported named types carry real GOROOT positions) and runs the
// analyzer against a pass with TypesInfo populated. The temp-dir filename keeps
// the fixture out of any path containing "/staticcheck/", which the analyzer's
// self-skip would otherwise exclude.
func runNoAnyAnalyzerOnSource(
	t *testing.T,
	packagePath string,
	filename string,
	source string,
) []analysis.Diagnostic {
	t.Helper()

	fset := token.NewFileSet()
	path := filepath.Join(t.TempDir(), filename)
	file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parser.ParseFile: %v", err)
	}

	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(packagePath, fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatalf("types.Config.Check: %v", err)
	}

	var diags []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer:  NoAnyOrEmptyInterfaceAnalyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
		Report:    func(d analysis.Diagnostic) { diags = append(diags, d) },
	}
	if _, err := NoAnyOrEmptyInterfaceAnalyzer.Run(pass); err != nil {
		t.Fatalf("NoAnyOrEmptyInterfaceAnalyzer.Run: %v", err)
	}
	return diags
}
