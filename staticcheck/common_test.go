package staticcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
)

func TestReportAtFallsBackFromGoBuildDescriptorPath(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	realSource := filepath.Join(t.TempDir(), "sample.go")
	file := parseLineDirectiveFile(t, fset, realSource)
	functionPosition := findFunctionPosition(t, file, "main")
	descriptorPath := filepath.ToSlash(fset.Position(functionPosition).Filename)
	if !isGoBuildCacheDescriptorPath(descriptorPath) {
		t.Fatalf("test setup function position = %q, want go-build descriptor path", descriptorPath)
	}

	var diagnostics []analysis.Diagnostic
	pass := &analysis.Pass{
		Fset: fset,
		Report: func(diagnostic analysis.Diagnostic) {
			diagnostics = append(diagnostics, diagnostic)
		},
	}
	reportAtf(pass, file, functionPosition, "test diagnostic")

	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1", len(diagnostics))
	}
	reportedPath := filepath.ToSlash(fset.Position(diagnostics[0].Pos).Filename)
	wantPath := filepath.ToSlash(realSource)
	if reportedPath != wantPath {
		t.Fatalf("diagnostic path = %q, want %q", reportedPath, wantPath)
	}
}

func TestMissingBoundaryLogReportsRealSourcePathForGoBuildDescriptorPosition(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	realSource := filepath.Join(t.TempDir(), "sample.go")
	file := parseLineDirectiveFile(t, fset, realSource)
	functionPosition := findFunctionPosition(t, file, "main")
	descriptorPath := filepath.ToSlash(fset.Position(functionPosition).Filename)
	if !isGoBuildCacheDescriptorPath(descriptorPath) {
		t.Fatalf("test setup function position = %q, want go-build descriptor path", descriptorPath)
	}

	var diagnostics []analysis.Diagnostic
	pass := &analysis.Pass{
		Fset:  fset,
		Files: []*ast.File{file},
		Pkg:   types.NewPackage("example.com/sample", "sample"),
		Report: func(diagnostic analysis.Diagnostic) {
			diagnostics = append(diagnostics, diagnostic)
		},
	}

	_, err := MissingBoundaryLogAnalyzer.Run(pass)
	if err != nil {
		t.Fatalf("MissingBoundaryLogAnalyzer.Run returned error: %v", err)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1", len(diagnostics))
	}
	reportedPath := filepath.ToSlash(fset.Position(diagnostics[0].Pos).Filename)
	wantPath := filepath.ToSlash(realSource)
	if reportedPath != wantPath {
		t.Fatalf("diagnostic path = %q, want %q", reportedPath, wantPath)
	}
}

func parseLineDirectiveFile(t *testing.T, fset *token.FileSet, realSource string) *ast.File {
	t.Helper()

	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("os.UserCacheDir returned error: %v", err)
	}
	descriptorPath := filepath.ToSlash(filepath.Join(userCacheDir, "go-build", "ab", "abcdef-d"))
	source := "package sample\n\n//" + "line " + descriptorPath + ":42\nfunc main() {}\n"
	file, err := parser.ParseFile(fset, realSource, source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parser.ParseFile returned error: %v", err)
	}
	return file
}

func findFunctionPosition(t *testing.T, file *ast.File, name string) token.Pos {
	t.Helper()

	for _, declaration := range file.Decls {
		functionDeclaration, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if functionDeclaration.Name.Name == name {
			return functionDeclaration.Pos()
		}
	}
	t.Fatalf("function %q was not found", name)
	return token.NoPos
}
