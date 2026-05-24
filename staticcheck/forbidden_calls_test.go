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

func TestTimeNowOutsideClockAllowsInternalClockPackage(t *testing.T) {
	t.Parallel()

	source := `package clock

import "time"

func Now() time.Time {
	return time.Now()
}

func Since(t time.Time) time.Duration {
	return time.Since(t)
}

func Until(t time.Time) time.Duration {
	return time.Until(t)
}
`
	diags := runTimeNowAnalyzerOnSource(t, "example.com/app/internal/clock", "clock.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics in internal/clock, got %d: %v", len(diags), diags)
	}
}

func TestTimeNowOutsideClockFlagsLibraryPackage(t *testing.T) {
	t.Parallel()

	source := `package service

import "time"

func Now() time.Time {
	return time.Now()
}
`
	diags := runTimeNowAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, "time.Now() outside internal/clock")
}

func TestTimeNowOutsideClockFlagsSinceInLibraryPackage(t *testing.T) {
	t.Parallel()

	source := `package service

import "time"

func Elapsed(t time.Time) time.Duration {
	return time.Since(t)
}
`
	diags := runTimeNowAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, "time.Since() outside internal/clock")
}

func TestTimeNowOutsideClockFlagsUntilInLibraryPackage(t *testing.T) {
	t.Parallel()

	source := `package service

import "time"

func Remaining(t time.Time) time.Duration {
	return time.Until(t)
}
`
	diags := runTimeNowAnalyzerOnSource(t, "example.com/app/internal/service", "service.go", source)
	wantOnce(t, diags, "time.Until() outside internal/clock")
}

func TestTimeNowOutsideClockAllowsMainPackage(t *testing.T) {
	t.Parallel()

	source := `package main

import "time"

func main() {
	_ = time.Now()
	_ = time.Since(time.Time{})
	_ = time.Until(time.Time{})
}
`
	diags := runTimeNowAnalyzerOnSource(t, "example.com/app/cmd/app", "main.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics in main package, got %d: %v", len(diags), diags)
	}
}

func TestTimeNowOutsideClockSkipsTestFiles(t *testing.T) {
	t.Parallel()

	source := `package service

import "time"
import "testing"

func TestNow(t *testing.T) {
	_ = time.Now()
	_ = time.Since(time.Time{})
	_ = time.Until(time.Time{})
}
`
	diags := runTimeNowAnalyzerOnSource(t, "example.com/app/internal/service", "service_test.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics in test file, got %d: %v", len(diags), diags)
	}
}

func runTimeNowAnalyzerOnSource(
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
		Analyzer: TimeNowOutsideClockAnalyzer,
		Fset:     fset,
		Files:    []*ast.File{file},
		Pkg:      types.NewPackage(packagePath, packageName),
		Report:   func(d analysis.Diagnostic) { diags = append(diags, d) },
	}
	if _, err := TimeNowOutsideClockAnalyzer.Run(pass); err != nil {
		t.Fatalf("TimeNowOutsideClockAnalyzer.Run: %v", err)
	}
	return diags
}
