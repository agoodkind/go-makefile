package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const (
	testSeamDirectivePrefix = "//testseam:external"
	testSeamMinReasonWords  = 3
	testSeamPackageScope    = "package scope"
	seamValueFunctionPrefix = "func:"
	seamValueSitePrefix     = "site:"
)

type seamFieldFact struct {
	Value string
}

func (*seamFieldFact) AFact() {}

func (fact *seamFieldFact) String() string { return "seam(" + fact.Value + ")" }

// TestSeamAnalyzer flags a _test.go file that replaces a function seam of the
// same module. A seam is a package-level variable of function or interface
// type declared in a non-test file, or a function-typed struct field that
// production code sets to exactly one value. A test with a replaced seam does
// not run the production function behind it.
//
// A field with no production assignment in its declaring package is not a
// seam. A field with several distinct production values is a callback and is
// not a seam. A clock or sleep function is a seam. The directive
// //testseam:external followed by a reason of at least three words, on the
// line above the replacement, exempts a replacement that fakes a service
// outside the host.
var TestSeamAnalyzer = &analysis.Analyzer{
	Name:      "testseam",
	Doc:       "rejects a test that replaces a function variable or a single-valued function field of the same module",
	Run:       runTestSeam,
	FactTypes: []analysis.Fact{(*seamFieldFact)(nil)},
}

type seamFinder struct {
	pass   *analysis.Pass
	fields map[*types.Var]string
	saved  map[*types.Var]*types.Var
}

func runTestSeam(pass *analysis.Pass) (any, error) {
	finder := &seamFinder{pass: pass, fields: productionSeamFields(pass)}
	for field, value := range finder.fields {
		if field.Pkg() == pass.Pkg {
			pass.ExportObjectFact(field, &seamFieldFact{Value: value})
		}
	}
	for _, file := range analyzableTestFiles(pass) {
		finder.inspectTestFile(file)
	}
	return nil, nil
}

func productionSeamFields(pass *analysis.Pass) map[*types.Var]string {
	values := make(map[*types.Var]map[string]bool)
	record := func(field *types.Var, value ast.Expr) {
		if field == nil || field.Pkg() != pass.Pkg {
			return
		}
		key, ok := seamValueKey(pass, value)
		if !ok {
			return
		}
		if values[field] == nil {
			values[field] = make(map[string]bool)
		}
		values[field][key] = true
	}
	for _, file := range pass.Files {
		if isTestFile(fileName(pass, file.Pos())) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			visitFieldValues(pass, node, record)
			return true
		})
	}
	seams := make(map[*types.Var]string)
	for field, keys := range values {
		if len(keys) != 1 {
			continue
		}
		for key := range keys {
			seams[field] = key
		}
	}
	return seams
}

func visitFieldValues(pass *analysis.Pass, node ast.Node, record func(*types.Var, ast.Expr)) {
	switch typed := node.(type) {
	case *ast.CompositeLit:
		for _, element := range typed.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok {
				continue
			}
			record(functionField(pass, key), pair.Value)
		}
	case *ast.AssignStmt:
		if typed.Tok != token.ASSIGN || len(typed.Lhs) != len(typed.Rhs) {
			return
		}
		for i, left := range typed.Lhs {
			selector, ok := left.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			record(functionField(pass, selector.Sel), typed.Rhs[i])
		}
	}
}

func functionField(pass *analysis.Pass, ident *ast.Ident) *types.Var {
	field, ok := pass.TypesInfo.Uses[ident].(*types.Var)
	if !ok || !field.IsField() {
		return nil
	}
	if _, isFunction := field.Type().Underlying().(*types.Signature); !isFunction {
		return nil
	}
	return field
}

// A named function or method has one key at every site. Any other expression has a key per site. A nil value
// has no key.
func seamValueKey(pass *analysis.Pass, value ast.Expr) (string, bool) {
	value = ast.Unparen(value)
	if typeAndValue, ok := pass.TypesInfo.Types[value]; ok && typeAndValue.IsNil() {
		return "", false
	}
	var ident *ast.Ident
	switch typed := value.(type) {
	case *ast.Ident:
		ident = typed
	case *ast.SelectorExpr:
		ident = typed.Sel
	}
	if ident != nil {
		if function, ok := pass.TypesInfo.Uses[ident].(*types.Func); ok {
			return seamValueFunctionPrefix + function.FullName(), true
		}
	}
	return seamValueSitePrefix + pass.Fset.Position(value.Pos()).String(), true
}

func (finder *seamFinder) inspectTestFile(file *ast.File) {
	directives := seamDirectiveLines(finder.pass, file)
	finder.saved = finder.savedVariables(file)
	for _, declaration := range file.Decls {
		scope := testSeamPackageScope
		if function, ok := declaration.(*ast.FuncDecl); ok {
			scope = function.Name.Name
		}
		ast.Inspect(declaration, func(node ast.Node) bool {
			finder.inspectNode(file, node, scope, directives)
			return true
		})
	}
}

func (finder *seamFinder) inspectNode(file *ast.File, node ast.Node, scope string, directives map[int]bool) {
	visitFieldValues(finder.pass, node, func(field *types.Var, value ast.Expr) {
		finder.checkField(file, field, value, scope, directives)
	})
	assign, ok := node.(*ast.AssignStmt)
	if !ok || assign.Tok != token.ASSIGN {
		return
	}
	for i, left := range assign.Lhs {
		if len(assign.Rhs) == len(assign.Lhs) && finder.restores(left, assign.Rhs[i]) {
			continue
		}
		finder.checkVariable(file, left, scope, directives)
	}
}

func (finder *seamFinder) savedVariables(file *ast.File) map[*types.Var]*types.Var {
	saved := make(map[*types.Var]*types.Var)
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != len(assign.Rhs) {
			return true
		}
		for i, left := range assign.Lhs {
			ident, ok := left.(*ast.Ident)
			if !ok {
				continue
			}
			local, ok := finder.pass.TypesInfo.ObjectOf(ident).(*types.Var)
			source := finder.packageVariable(assign.Rhs[i])
			if ok && source != nil && local.Parent() != local.Pkg().Scope() {
				saved[local] = source
			}
		}
		return true
	})
	return saved
}

func (finder *seamFinder) restores(left, right ast.Expr) bool {
	target := finder.packageVariable(left)
	ident, ok := ast.Unparen(right).(*ast.Ident)
	if target == nil || !ok {
		return false
	}
	local, ok := finder.pass.TypesInfo.Uses[ident].(*types.Var)
	return ok && finder.saved[local] == target
}

func (finder *seamFinder) packageVariable(expr ast.Expr) *types.Var {
	var ident *ast.Ident
	switch typed := ast.Unparen(expr).(type) {
	case *ast.Ident:
		ident = typed
	case *ast.SelectorExpr:
		ident = typed.Sel
	default:
		return nil
	}
	variable, ok := finder.pass.TypesInfo.Uses[ident].(*types.Var)
	if !ok || variable.IsField() || variable.Pkg() == nil || variable.Parent() != variable.Pkg().Scope() {
		return nil
	}
	return variable
}

func (finder *seamFinder) checkField(file *ast.File, field *types.Var, value ast.Expr, scope string, directives map[int]bool) {
	if field == nil {
		return
	}
	production, isSeam := finder.seamValue(field)
	if !isSeam {
		return
	}
	key, ok := seamValueKey(finder.pass, value)
	if !ok || key == production {
		return
	}
	if finder.exempt(value.Pos(), directives) {
		return
	}
	reportAtf(
		finder.pass, file, value.Pos(),
		"%s replaces the function field %s. Production code sets that field to one value. A test must run the production function with real dependencies. Remove the replacement, or add //testseam:external <reason> above it when the function calls a service outside the host.",
		scope, field.Name(),
	)
}

func (finder *seamFinder) seamValue(field *types.Var) (string, bool) {
	if field.Pkg() == nil || !sameModulePackage(finder.pass, field.Pkg().Path()) {
		return "", false
	}
	if field.Pkg() == finder.pass.Pkg {
		value, ok := finder.fields[field]
		return value, ok
	}
	var fact seamFieldFact
	if finder.pass.ImportObjectFact(field, &fact) {
		return fact.Value, true
	}
	return "", false
}

func (finder *seamFinder) checkVariable(file *ast.File, left ast.Expr, scope string, directives map[int]bool) {
	var ident *ast.Ident
	switch typed := left.(type) {
	case *ast.Ident:
		ident = typed
	case *ast.SelectorExpr:
		ident = typed.Sel
	default:
		return
	}
	variable, ok := finder.pass.TypesInfo.Uses[ident].(*types.Var)
	if !ok || variable.IsField() || variable.Pkg() == nil || variable.Parent() != variable.Pkg().Scope() {
		return
	}
	if isTestFile(fileName(finder.pass, variable.Pos())) || !sameModulePackage(finder.pass, variable.Pkg().Path()) {
		return
	}
	switch variable.Type().Underlying().(type) {
	case *types.Signature, *types.Interface:
	default:
		return
	}
	if finder.exempt(left.Pos(), directives) {
		return
	}
	reportAtf(
		finder.pass, file, left.Pos(),
		"%s replaces the package variable %s. A test must run the production value with real dependencies. Remove the replacement, or add //testseam:external <reason> above it when the value calls a service outside the host.",
		scope, variable.Name(),
	)
}

func (finder *seamFinder) exempt(pos token.Pos, directives map[int]bool) bool {
	line := finder.pass.Fset.Position(pos).Line
	return directives[line] || directives[line-1]
}

func seamDirectiveLines(pass *analysis.Pass, file *ast.File) map[int]bool {
	lines := make(map[int]bool)
	for _, group := range file.Comments {
		for _, comment := range group.List {
			reason, ok := strings.CutPrefix(comment.Text, testSeamDirectivePrefix)
			if !ok {
				continue
			}
			// A second comment marker ends the reason.
			reason, _, _ = strings.Cut(reason, "//")
			if len(strings.Fields(reason)) < testSeamMinReasonWords {
				reportAtf(
					pass, file, comment.Pos(),
					"The //testseam:external directive needs a reason of at least three words that states which service outside the host the replacement fakes.",
				)
				continue
			}
			lines[pass.Fset.Position(comment.Pos()).Line] = true
		}
	}
	return lines
}

// sameModulePackage compares against the module path when the driver supplies
// it. Without module information, only the package under analysis and its
// external test package count.
func sameModulePackage(pass *analysis.Pass, path string) bool {
	path = strings.TrimSuffix(path, externalTestPathSuffix)
	module := pass.Module
	if module != nil && module.Path != "" {
		return path == module.Path || strings.HasPrefix(path, module.Path+"/")
	}
	return path == strings.TrimSuffix(packagePath(pass), externalTestPathSuffix)
}
