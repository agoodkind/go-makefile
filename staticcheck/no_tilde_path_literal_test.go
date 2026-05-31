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

func TestNoTildePathLiteralFlagsBareTilde(t *testing.T) {
	t.Parallel()

	source := `package service

func Path() string {
	return "~"
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, `hardcoded home directory "~"`)
}

func TestNoTildePathLiteralFlagsTildeSlashPath(t *testing.T) {
	t.Parallel()

	source := `package service

func ConfigPath() string {
	return "~/.config/app"
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, `hardcoded home directory "~/.config/app"`)
}

func TestNoTildePathLiteralFlagsRawString(t *testing.T) {
	t.Parallel()

	source := "package service\n\nfunc ConfigPath() string {\n\treturn `~/.config/app`\n}\n"
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, `hardcoded home directory "~/.config/app"`)
}

func TestNoTildePathLiteralIgnoresMiddleTilde(t *testing.T) {
	t.Parallel()

	source := `package service

func Token() string {
	return "a~b"
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for middle-tilde literal, got %d: %v", len(diags), diags)
	}
}

func TestNoTildePathLiteralSkipsTestFiles(t *testing.T) {
	t.Parallel()

	source := `package service

func ConfigPath() string {
	return "~/.config/app"
}
`
	diags := runNoTildePathLiteralAnalyzerOnSource(t, "example.com/app/internal/service", "service_test.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics in test file, got %d: %v", len(diags), diags)
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
