package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// StringSwitchShouldBeEnumAnalyzer flags switch statements that switch on
// a bare `string` value (not a named string type) with two or more
// string-literal cases. Such a switch enumerates a closed set of expected
// values inline; the closed set should be modelled as a named enum type
// with a const block, not as ad-hoc string literals scattered through
// the code.
//
// Skipped for: test files, generated files, protobuf-generated files,
// and the staticcheck-extra source itself.
//
// Pattern caught:
//
//	switch role {                  // role is a bare `string`
//	case "admin":     ...
//	case "operator":  ...
//	case "viewer":    ...
//	}
//
// Right shape:
//
//	type Role string
//	const (
//	    RoleAdmin    Role = "admin"
//	    RoleOperator Role = "operator"
//	    RoleViewer   Role = "viewer"
//	)
//
//	switch role {
//	case RoleAdmin:    ...
//	case RoleOperator: ...
//	case RoleViewer:   ...
//	}
//
// The named type also unlocks the `exhaustive` linter so future case
// additions do not silently slip past callers that switch over the type.
//
// Not flagged: switches over named string types (already enums), type
// switches (`switch v.(type)`), tag-less switches with mixed boolean
// expressions, switches with any non-literal case, switches with fewer
// than two cases.
var StringSwitchShouldBeEnumAnalyzer = &analysis.Analyzer{
	Name: "string_switch_should_be_enum",
	Doc:  "rejects bare-string switches with two or more string-literal cases; declare a named enum type",
	Run:  runStringSwitchShouldBeEnum,
}

func runStringSwitchShouldBeEnum(pass *analysis.Pass) (any, error) {
	if isStaticcheckPackage(pass) {
		return nil, nil
	}
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file, path) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		inspectStringSwitches(pass, file)
	}
	return nil, nil
}

func inspectStringSwitches(pass *analysis.Pass, file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		reportBareStringSwitch(pass, sw)
		return true
	})
}

func reportBareStringSwitch(pass *analysis.Pass, sw *ast.SwitchStmt) {
	if !switchHasBareStringTag(pass, sw) {
		return
	}
	stringLitCases, otherCases := countStringSwitchCases(sw)
	if stringLitCases < 2 || otherCases > 0 {
		return
	}
	pass.Reportf(sw.Pos(),
		"switch on bare string with %d string-literal cases; declare a named enum type and switch on its constants",
		stringLitCases)
}

func switchHasBareStringTag(pass *analysis.Pass, sw *ast.SwitchStmt) bool {
	if sw == nil || sw.Tag == nil || sw.Body == nil {
		return false
	}
	tagType := pass.TypesInfo.TypeOf(sw.Tag)
	if tagType == nil {
		return false
	}
	// Only flag bare `string`. A named string type (type Role string)
	// is already the enum shape we want.
	basic, ok := tagType.(*types.Basic)
	return ok && basic.Kind() == types.String
}

func countStringSwitchCases(sw *ast.SwitchStmt) (int, int) {
	stringLitCases := 0
	otherCases := 0
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok || cc.List == nil {
			continue
		}
		for _, expr := range cc.List {
			if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				stringLitCases++
			} else {
				otherCases++
			}
		}
	}
	return stringLitCases, otherCases
}
