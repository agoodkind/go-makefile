package staticcheck

import (
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const (
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
