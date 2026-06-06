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

func TestNoTildeDisplayReturnNotFlagged(t *testing.T) {
	t.Parallel()

	source := `package service

func Path() string {
	return "~"
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantNoDiagnostics(t, diags)
}

func TestNoTildeDisplayReturnSlashNotFlagged(t *testing.T) {
	t.Parallel()

	source := `package service

func ConfigPath() string {
	return "~/.config/app"
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantNoDiagnostics(t, diags)
}

func TestNoTildeFlagsDirectPathArg(t *testing.T) {
	t.Parallel()

	source := `package service

import "os"

func Load() {
	_, _ = os.Open("~/.config/app")
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, `hardcoded home directory "~/.config/app"`)
}

func TestNoTildeFlagsFilepathJoinArg(t *testing.T) {
	t.Parallel()

	source := `package service

import "path/filepath"

func Build() string {
	return filepath.Join("~", "config")
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, `hardcoded home directory "~"`)
}

func TestNoTildeFlagsLocalAssignedThenPathArg(t *testing.T) {
	t.Parallel()

	source := `package service

import "os"

func Load() {
	home := "~/.config/app"
	_, _ = os.Open(home)
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, `hardcoded home directory "~/.config/app"`)
}

func TestNoTildeIgnoresLocalAssignedNotUsedInPath(t *testing.T) {
	t.Parallel()

	source := `package service

func Describe() string {
	label := "~/.config/app"
	return "configured at " + label
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantNoDiagnostics(t, diags)
}

func TestNoTildeIgnoresMiddleTilde(t *testing.T) {
	t.Parallel()

	source := `package service

import "os"

func Token() {
	_, _ = os.Open("a~b")
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantNoDiagnostics(t, diags)
}

func TestNoTildeSkipsTestFiles(t *testing.T) {
	t.Parallel()

	source := `package service

import "os"

func Load() {
	_, _ = os.Open("~/.config/app")
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service_test.go", source)
	wantNoDiagnostics(t, diags)
}

func wantNoDiagnostics(t *testing.T, diags []analysis.Diagnostic) {
	t.Helper()
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %d: %v", len(diags), diags)
	}
}

func runNoTildePathLiteralAnalyzerOnSource(
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

	var packageName string
	if file.Name != nil {
		packageName = file.Name.Name
	} else {
		packageName = strings.TrimSuffix(filename, ".go")
	}

	var diags []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer: NoTildePathLiteralAnalyzer,
		Fset:     fset,
		Files:    []*ast.File{file},
		Pkg:      types.NewPackage(packagePath, packageName),
		Report:   func(d analysis.Diagnostic) { diags = append(diags, d) },
	}
	if _, err := NoTildePathLiteralAnalyzer.Run(pass); err != nil {
		t.Fatalf("NoTildePathLiteralAnalyzer.Run: %v", err)
	}
	return diags
}
