package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
)

const (
	testDoubleDirectivePrefix = "//testdouble:external"
	testDoubleMinReasonWords  = 3
	externalTestPathSuffix    = "_test"
)

// mockLibraryPaths lists the import paths of mock generation and mock runtime
// libraries. The import check matches the path text because the library is the
// signal, not a type in the package.
var mockLibraryPaths = []string{
	"github.com/golang/mock",
	"go.uber.org/mock",
	"github.com/stretchr/testify/mock",
	"github.com/vektra/mockery",
	"github.com/maxbrunsfeld/counterfeiter",
}

// TestDoubleAnalyzer flags a named type declared in a _test.go file that stands
// in for an interface of the same module, and flags the import of a mock
// library in a _test.go file. A test with a hand-written double or a generated
// mock does not exercise the production implementation. Such a test can pass
// while the real dependency is broken. The required form runs the production type.
//
// A type that fakes a service outside the host has the directive
// //testdouble:external followed by a reason of at least three words. The
// directive does not apply to a mock library import.
var TestDoubleAnalyzer = &analysis.Analyzer{
	Name: "testdouble",
	Doc:  "rejects test double types for same-module interfaces and mock library imports in _test.go files",
	Run:  runTestDouble,
}

type testTypeDecl struct {
	spec      *ast.TypeSpec
	file      *ast.File
	directive *ast.Comment
}

type testDoubleFinder struct {
	pass      *analysis.Pass
	decls     map[*types.TypeName]*testTypeDecl
	converted map[*types.TypeName]*types.TypeName
}

func runTestDouble(pass *analysis.Pass) (any, error) {
	files := analyzableTestFiles(pass)
	reportMockImports(pass, files)

	finder := &testDoubleFinder{
		pass:      pass,
		decls:     make(map[*types.TypeName]*testTypeDecl),
		converted: make(map[*types.TypeName]*types.TypeName),
	}
	ordered := finder.collectDeclarations(files)
	if len(ordered) == 0 {
		return nil, nil
	}
	for _, file := range files {
		finder.inspectFile(file)
	}
	finder.report(ordered)
	return nil, nil
}

func reportMockImports(pass *analysis.Pass, files []*ast.File) {
	for _, file := range files {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil || !isMockLibraryPath(path) {
				continue
			}
			reportAtf(
				pass, file, spec.Path.Pos(),
				"The import %q is a mock library. A test must run the production implementation with real dependencies. Remove the mock and run the production implementation.",
				path,
			)
		}
	}
}

func isMockLibraryPath(path string) bool {
	for _, library := range mockLibraryPaths {
		if path == library || strings.HasPrefix(path, library+"/") {
			return true
		}
	}
	return false
}

func (finder *testDoubleFinder) collectDeclarations(files []*ast.File) []*types.TypeName {
	var ordered []*types.TypeName
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			declaration, ok := node.(*ast.GenDecl)
			if !ok || declaration.Tok != token.TYPE {
				return true
			}
			for _, spec := range declaration.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				obj := finder.concreteTypeName(typeSpec)
				if obj == nil {
					continue
				}
				directive := findTestDoubleDirective(typeSpec.Doc)
				if directive == nil && len(declaration.Specs) == 1 {
					directive = findTestDoubleDirective(declaration.Doc)
				}
				finder.decls[obj] = &testTypeDecl{spec: typeSpec, file: file, directive: directive}
				ordered = append(ordered, obj)
			}
			return true
		})
	}
	return ordered
}

func (finder *testDoubleFinder) concreteTypeName(spec *ast.TypeSpec) *types.TypeName {
	obj, ok := finder.pass.TypesInfo.Defs[spec.Name].(*types.TypeName)
	if !ok || obj.IsAlias() {
		return nil
	}
	if _, isInterface := obj.Type().Underlying().(*types.Interface); isInterface {
		return nil
	}
	return obj
}

func findTestDoubleDirective(group *ast.CommentGroup) *ast.Comment {
	if group == nil {
		return nil
	}
	for _, comment := range group.List {
		rest, ok := strings.CutPrefix(comment.Text, testDoubleDirectivePrefix)
		if !ok {
			continue
		}
		if rest == "" || unicode.IsSpace([]rune(rest)[0]) {
			return comment
		}
	}
	return nil
}

func directiveHasReason(comment *ast.Comment) bool {
	reason := strings.TrimPrefix(comment.Text, testDoubleDirectivePrefix)
	return len(strings.Fields(reason)) >= testDoubleMinReasonWords
}

func (finder *testDoubleFinder) report(ordered []*types.TypeName) {
	for _, obj := range ordered {
		decl := finder.decls[obj]
		validDirective := false
		if decl.directive != nil {
			validDirective = directiveHasReason(decl.directive)
			if !validDirective {
				reportAtf(
					finder.pass, decl.file, decl.directive.Pos(),
					"The //testdouble:external directive needs a reason of at least three words that states which service outside the host the type fakes.",
				)
			}
		}
		target, converted := finder.converted[obj]
		if !converted || validDirective || wrapsInterface(obj, target) {
			continue
		}
		reportAtf(
			finder.pass, decl.file, decl.spec.Name.Pos(),
			"The type %s is a test double for the interface %s.%s. A test must run the production implementation with real dependencies. Use the production type, or add //testdouble:external <reason> above the type when it fakes a service outside the host.",
			obj.Name(), target.Pkg().Name(), target.Name(),
		)
	}
}

// A struct with a field of the interface type delegates to a real
// implementation and adds a fault or a recorder around it.
func wrapsInterface(obj, target *types.TypeName) bool {
	structType, ok := obj.Type().Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := range structType.NumFields() {
		if types.Identical(structType.Field(i).Type(), target.Type()) {
			return true
		}
	}
	return false
}

func (finder *testDoubleFinder) inspectFile(file *ast.File) {
	var stack []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		switch typed := node.(type) {
		case *ast.CallExpr:
			finder.checkCall(typed)
		case *ast.AssignStmt:
			finder.checkAssign(typed)
		case *ast.ValueSpec:
			finder.checkValueSpec(typed)
		case *ast.CompositeLit:
			finder.checkCompositeLit(typed)
		case *ast.ReturnStmt:
			finder.checkReturn(typed, stack)
		}
		return true
	})
}

func (finder *testDoubleFinder) checkCall(call *ast.CallExpr) {
	info := finder.pass.TypesInfo
	if info.Types[call.Fun].IsType() {
		if len(call.Args) == 1 {
			finder.check(info.TypeOf(call.Fun), call.Args[0])
		}
		return
	}
	funType := info.TypeOf(call.Fun)
	if funType == nil || call.Ellipsis.IsValid() {
		return
	}
	signature, ok := types.Unalias(funType).Underlying().(*types.Signature)
	if !ok {
		return
	}
	params := signature.Params()
	for i, arg := range call.Args {
		finder.check(parameterType(signature, params, i), arg)
	}
}

func parameterType(signature *types.Signature, params *types.Tuple, index int) types.Type {
	last := params.Len() - 1
	if signature.Variadic() && index >= last {
		slice, ok := params.At(last).Type().(*types.Slice)
		if !ok {
			return nil
		}
		return slice.Elem()
	}
	if index > last {
		return nil
	}
	return params.At(index).Type()
}

func (finder *testDoubleFinder) checkAssign(assign *ast.AssignStmt) {
	if len(assign.Lhs) != len(assign.Rhs) {
		return
	}
	for i, value := range assign.Rhs {
		finder.check(finder.pass.TypesInfo.TypeOf(assign.Lhs[i]), value)
	}
}

func (finder *testDoubleFinder) checkValueSpec(spec *ast.ValueSpec) {
	if spec.Type == nil {
		return
	}
	target := finder.pass.TypesInfo.TypeOf(spec.Type)
	for _, value := range spec.Values {
		finder.check(target, value)
	}
}

func (finder *testDoubleFinder) checkCompositeLit(lit *ast.CompositeLit) {
	literalType := finder.pass.TypesInfo.TypeOf(lit)
	if literalType == nil {
		return
	}
	switch underlying := types.Unalias(literalType).Underlying().(type) {
	case *types.Struct:
		finder.checkStructLit(lit, underlying)
	case *types.Slice:
		finder.checkElements(lit, nil, underlying.Elem())
	case *types.Array:
		finder.checkElements(lit, nil, underlying.Elem())
	case *types.Map:
		finder.checkElements(lit, underlying.Key(), underlying.Elem())
	}
}

func (finder *testDoubleFinder) checkStructLit(lit *ast.CompositeLit, structType *types.Struct) {
	for i, element := range lit.Elts {
		pair, keyed := element.(*ast.KeyValueExpr)
		if !keyed {
			if i < structType.NumFields() {
				finder.check(structType.Field(i).Type(), element)
			}
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok {
			continue
		}
		field, ok := finder.pass.TypesInfo.Uses[key].(*types.Var)
		if ok {
			finder.check(field.Type(), pair.Value)
		}
	}
}

func (finder *testDoubleFinder) checkElements(lit *ast.CompositeLit, keyType, elementType types.Type) {
	for _, element := range lit.Elts {
		pair, keyed := element.(*ast.KeyValueExpr)
		if !keyed {
			finder.check(elementType, element)
			continue
		}
		finder.check(keyType, pair.Key)
		finder.check(elementType, pair.Value)
	}
}

func (finder *testDoubleFinder) checkReturn(statement *ast.ReturnStmt, stack []ast.Node) {
	signature := finder.enclosingSignature(stack)
	if signature == nil || signature.Results().Len() != len(statement.Results) {
		return
	}
	for i, result := range statement.Results {
		finder.check(signature.Results().At(i).Type(), result)
	}
}

func (finder *testDoubleFinder) enclosingSignature(stack []ast.Node) *types.Signature {
	for i := len(stack) - 1; i >= 0; i-- {
		switch function := stack[i].(type) {
		case *ast.FuncLit:
			signature, _ := finder.pass.TypesInfo.TypeOf(function).(*types.Signature)
			return signature
		case *ast.FuncDecl:
			signature, _ := finder.pass.TypesInfo.TypeOf(function.Name).(*types.Signature)
			return signature
		}
	}
	return nil
}

func (finder *testDoubleFinder) check(target types.Type, value ast.Expr) {
	if target == nil {
		return
	}
	interfaceName := finder.sameModuleInterface(target)
	if interfaceName == nil {
		return
	}
	valueName := finder.testTypeName(finder.pass.TypesInfo.TypeOf(value))
	if valueName == nil {
		return
	}
	if _, seen := finder.converted[valueName]; !seen {
		finder.converted[valueName] = interfaceName
	}
}

func (finder *testDoubleFinder) testTypeName(valueType types.Type) *types.TypeName {
	if valueType == nil {
		return nil
	}
	valueType = types.Unalias(valueType)
	if pointer, ok := valueType.(*types.Pointer); ok {
		valueType = types.Unalias(pointer.Elem())
	}
	named, ok := valueType.(*types.Named)
	if !ok {
		return nil
	}
	obj := named.Origin().Obj()
	if _, declared := finder.decls[obj]; !declared {
		return nil
	}
	return obj
}

func (finder *testDoubleFinder) sameModuleInterface(target types.Type) *types.TypeName {
	named, ok := types.Unalias(target).(*types.Named)
	if !ok {
		return nil
	}
	if _, isInterface := named.Underlying().(*types.Interface); !isInterface {
		return nil
	}
	obj := named.Origin().Obj()
	if obj.Pkg() == nil || isTestFile(fileName(finder.pass, obj.Pos())) {
		return nil
	}
	if !finder.inSameModule(obj.Pkg().Path()) {
		return nil
	}
	return obj
}

// inSameModule compares against the module path when the driver supplies it.
// Without module information, only the package under analysis and its external
// test package count.
func (finder *testDoubleFinder) inSameModule(path string) bool {
	module := finder.pass.Module
	if module != nil && module.Path != "" {
		return path == module.Path || strings.HasPrefix(path, module.Path+"/")
	}
	underTest := strings.TrimSuffix(packagePath(finder.pass), externalTestPathSuffix)
	return path == underTest
}
