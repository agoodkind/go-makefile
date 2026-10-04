package staticcheck

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

const (
	testMainFunctionName      = "TestMain"
	externalTestPackageSuffix = "_test"
	exportTestFileName        = "export_test.go"
	mainPackageName           = "main"
)

// TestPackageAnalyzer flags a _test.go file that declares the package under
// test. A test in the package under test can call unexported identifiers and
// pass while the exported behavior is broken. The required form is the external
// test package <name>_test, which compiles against the exported API only.
//
// A file named export_test.go is exempt. It is the standard place to expose an
// unexported identifier to the external test package.
var TestPackageAnalyzer = &analysis.Analyzer{
	Name: "testpackage",
	Doc:  "rejects a _test.go file in the package under test; use the external <name>_test package",
	Run:  runTestPackage,
}

func runTestPackage(pass *analysis.Pass) (any, error) {
	for _, file := range analyzableTestFiles(pass) {
		path := fileName(pass, file.Pos())
		if filepath.Base(path) == exportTestFileName {
			continue
		}
		packageName := file.Name.Name
		if strings.HasSuffix(packageName, externalTestPackageSuffix) {
			continue
		}
		reportUnexportedCalls(pass, file)
		if packageName == mainPackageName {
			reportAtf(
				pass, file, file.Name.Pos(),
				"This test file declares package main, the package under test. A test of a command must enter through the built command. Change the package clause to main_test and run the command.",
			)
			continue
		}
		reportAtf(
			pass, file, file.Name.Pos(),
			"This test file declares package %s, the package under test. A test must enter through the exported API. Change the package clause to %s_test and call only exported identifiers.",
			packageName, packageName,
		)
	}
	return nil, nil
}

// reportUnexportedCalls reports each test function that calls an unexported
// function or method declared in a non-test file of the package under test.
// The file finding has one baseline row per file. This finding has one row per
// test function, and a new test in an older file is reported.
func reportUnexportedCalls(pass *analysis.Pass, file *ast.File) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || function.Recv != nil {
			continue
		}
		name := function.Name.Name
		if !strings.HasPrefix(name, testFunctionPrefix) || name == testMainFunctionName {
			continue
		}
		if !callsUnexportedProduction(pass, function.Body) {
			continue
		}
		reportAtf(
			pass, file, function.Name.Pos(),
			"Test %s calls unexported functions of the package under test. A test must enter through the exported API or the built command. Call the exported entry point that uses those functions.",
			name,
		)
	}
}

func callsUnexportedProduction(pass *analysis.Pass, body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
		if !ok || callee.Exported() || callee.Pkg() != pass.Pkg {
			return true
		}
		if !isTestFile(fileName(pass, callee.Pos())) {
			found = true
		}
		return !found
	})
	return found
}
