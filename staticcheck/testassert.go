package staticcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

const (
	testingPackagePath   = "testing"
	testFunctionPrefix   = "Test"
	testTypeName         = "T"
	testInterfaceName    = "TB"
	subtestMethodName    = "Run"
	subtestLiteralMinArg = 2
)

var failingTestingMethods = map[string]bool{
	"Error":   true,
	"Errorf":  true,
	"Fatal":   true,
	"Fatalf":  true,
	"Fail":    true,
	"FailNow": true,
}

var skippingTestingMethods = map[string]bool{
	"Skip":    true,
	"Skipf":   true,
	"SkipNow": true,
}

// TestAssertAnalyzer flags a test function or a t.Run function literal with no
// call able to fail the test. A test that only calls production code passes
// whatever the code returns. The required form is an assertion on an outcome of
// the code under test through t.Error, t.Fatal, or a helper with a testing.TB
// parameter.
//
// A call with an argument implementing testing.TB counts as able to fail the
// test. Such a helper can call t.Fatal. A failing call inside a nested function
// literal, defer, goroutine, loop, or branch counts. A body made only of t.Skip,
// t.Skipf, or t.SkipNow calls is exempt.
var TestAssertAnalyzer = &analysis.Analyzer{
	Name: "testassert",
	Doc:  "rejects a test or subtest with no statement able to fail it; assert an outcome with t.Error, t.Fatal, or a testing.TB helper",
	Run:  runTestAssert,
}

func runTestAssert(pass *analysis.Pass) (any, error) {
	tbInterface := testingInterface(pass)
	if tbInterface == nil {
		return nil, nil
	}
	checker := assertionChecker{pass: pass, tbInterface: tbInterface}
	for _, file := range analyzableTestFiles(pass) {
		checker.checkFile(file)
	}
	return nil, nil
}

type assertionChecker struct {
	pass        *analysis.Pass
	tbInterface *types.Interface
}

func testingInterface(pass *analysis.Pass) *types.Interface {
	for _, imported := range pass.Pkg.Imports() {
		if imported.Path() != testingPackagePath {
			continue
		}
		object := imported.Scope().Lookup(testInterfaceName)
		if object == nil {
			return nil
		}
		tbInterface, ok := object.Type().Underlying().(*types.Interface)
		if !ok {
			return nil
		}
		return tbInterface
	}
	return nil
}

func (checker assertionChecker) checkFile(file *ast.File) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || !checker.isTestFunction(function) {
			continue
		}
		if checker.canFail(function.Body) {
			if checker.assertsOnlyErrorNilness(function.Body) {
				reportAtf(
					checker.pass, file, function.Name.Pos(),
					"Test %s asserts only that an error is nil or not nil. A test must assert an outcome of the code under test. Add a check on the returned value, on the state that the call changed, or on the kind of the error.",
					function.Name.Name,
				)
			}
			continue
		}
		reportAtf(
			checker.pass, file, function.Name.Pos(),
			"Test %s has no statement able to fail the test. A test must assert an outcome of the code under test. Add a check that calls t.Error, t.Fatal, or a helper with a testing.TB parameter.",
			function.Name.Name,
		)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		literal := checker.subtestLiteral(call)
		if literal == nil || checker.canFail(literal.Body) {
			return true
		}
		reportAtf(
			checker.pass, file, literal.Pos(),
			"This subtest has no statement able to fail the subtest. A subtest must assert an outcome of the code under test. Add a check that calls t.Error, t.Fatal, or a helper with a testing.TB parameter.",
		)
		return true
	})
}

func (checker assertionChecker) isTestFunction(function *ast.FuncDecl) bool {
	if function.Recv != nil || !strings.HasPrefix(function.Name.Name, testFunctionPrefix) {
		return false
	}
	parameters := function.Type.Params.List
	if len(parameters) != 1 || len(parameters[0].Names) > 1 {
		return false
	}
	pointer, ok := checker.pass.TypesInfo.TypeOf(parameters[0].Type).(*types.Pointer)
	if !ok {
		return false
	}
	return isTestingNamed(pointer.Elem(), testTypeName)
}

func isTestingNamed(typ types.Type, name string) bool {
	named, ok := typ.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Pkg().Path() == testingPackagePath && named.Obj().Name() == name
}

// subtestLiteral returns the function literal passed to (*testing.T).Run.
func (checker assertionChecker) subtestLiteral(call *ast.CallExpr) *ast.FuncLit {
	if !checker.isTestRunCall(call) || len(call.Args) < subtestLiteralMinArg {
		return nil
	}
	literal, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
	if !ok {
		return nil
	}
	return literal
}

// testingMethod returns the callee when it is a method declared in package
// testing, including methods promoted through the testing types.
func (checker assertionChecker) testingMethod(call *ast.CallExpr) *types.Func {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	method, ok := checker.pass.TypesInfo.Uses[selector.Sel].(*types.Func)
	if !ok || method.Pkg() == nil || method.Pkg().Path() != testingPackagePath {
		return nil
	}
	signature, ok := method.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return nil
	}
	return method
}

func (checker assertionChecker) isTestRunCall(call *ast.CallExpr) bool {
	method := checker.testingMethod(call)
	if method == nil || method.Name() != subtestMethodName {
		return false
	}
	signature, ok := method.Type().(*types.Signature)
	if !ok {
		return false
	}
	receiver := signature.Recv().Type()
	if pointer, isPointer := receiver.(*types.Pointer); isPointer {
		receiver = pointer.Elem()
	}
	return isTestingNamed(receiver, testTypeName)
}

// canFail reports whether the body has a call able to fail the test, or consists
// only of skip calls.
func (checker assertionChecker) canFail(body *ast.BlockStmt) bool {
	if checker.onlySkips(body) {
		return true
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if ok && checker.isFailingCall(call) {
			found = true
		}
		return !found
	})
	return found
}

// assertsOnlyErrorNilness reports whether every failing call of the body is in
// an if statement that compares an error value with nil, and no helper
// receives a testing.TB value. A helper can assert an outcome.
//
// A test that only requires a nil error is reported when it discards another
// result of the call with the blank identifier. A call with an error as its
// only result has no other outcome to assert. A test that only requires a
// non-nil error is reported: a failure for another reason passes it.
func (checker assertionChecker) assertsOnlyErrorNilness(body *ast.BlockStmt) bool {
	failingCalls := 0
	onlyNilness := true
	expectsFailure := false
	var conditions []ast.Expr
	var visit func(node ast.Node)
	visit = func(node ast.Node) {
		if node == nil || !onlyNilness {
			return
		}
		if statement, ok := node.(*ast.IfStmt); ok {
			if statement.Init != nil {
				visit(statement.Init)
			}
			visit(statement.Cond)
			conditions = append(conditions, statement.Cond)
			visit(statement.Body)
			conditions = conditions[:len(conditions)-1]
			if statement.Else != nil {
				visit(statement.Else)
			}
			return
		}
		if call, ok := node.(*ast.CallExpr); ok {
			method := checker.testingMethod(call)
			switch {
			case method != nil && failingTestingMethods[method.Name()]:
				failingCalls++
				if len(conditions) == 0 || !checker.isErrorNilComparison(conditions[len(conditions)-1]) {
					onlyNilness = false
				} else if binary, isBinary := ast.Unparen(conditions[len(conditions)-1]).(*ast.BinaryExpr); isBinary && binary.Op == token.EQL {
					expectsFailure = true
				}
			case method == nil && checker.passesTestingValue(call):
				onlyNilness = false
			}
		}
		for _, child := range childNodes(node) {
			visit(child)
		}
	}
	visit(body)
	if !onlyNilness || failingCalls == 0 || checker.hasOtherOutcomeCheck(body) {
		return false
	}
	return expectsFailure || checker.discardsResult(body)
}

// hasOtherOutcomeCheck reports whether the body checks file state with os.Stat
// or os.Lstat, or stores a testing.TB value in a composite literal. The error
// of a stat call is the assertion on the file. A stored testing.TB value lets
// a method of the literal fail the test.
func (checker assertionChecker) hasOtherOutcomeCheck(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CallExpr:
			function, ok := typeutil.Callee(checker.pass.TypesInfo, typed).(*types.Func)
			if ok && function.Pkg() != nil && function.Pkg().Path() == "os" &&
				(function.Name() == "Stat" || function.Name() == "Lstat") {
				found = true
			}
		case *ast.CompositeLit:
			for _, element := range typed.Elts {
				if pair, ok := element.(*ast.KeyValueExpr); ok {
					element = pair.Value
				}
				elementType := checker.pass.TypesInfo.TypeOf(element)
				if elementType != nil && types.Implements(elementType, checker.tbInterface) {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// discardsResult reports whether the body assigns a call with an error result
// and discards another result of that call with the blank identifier.
func (checker assertionChecker) discardsResult(body *ast.BlockStmt) bool {
	found := false
	errorType := types.Universe.Lookup("error").Type()
	ast.Inspect(body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) < 2 {
			return !found
		}
		if _, isCall := assign.Rhs[0].(*ast.CallExpr); !isCall {
			return true
		}
		tuple, ok := checker.pass.TypesInfo.TypeOf(assign.Rhs[0]).(*types.Tuple)
		if !ok || tuple.Len() != len(assign.Lhs) {
			return true
		}
		hasError := false
		hasBlank := false
		for i, left := range assign.Lhs {
			if types.Identical(tuple.At(i).Type(), errorType) {
				hasError = true
				continue
			}
			if ident, isIdent := left.(*ast.Ident); isIdent && ident.Name == "_" {
				hasBlank = true
			}
		}
		if hasError && hasBlank {
			found = true
		}
		return !found
	})
	return found
}

// childNodes returns the direct children of a node in source order.
func childNodes(node ast.Node) []ast.Node {
	var children []ast.Node
	first := true
	ast.Inspect(node, func(child ast.Node) bool {
		if first {
			first = false
			return true
		}
		if child != nil {
			children = append(children, child)
		}
		return false
	})
	return children
}

func (checker assertionChecker) isErrorNilComparison(condition ast.Expr) bool {
	binary, ok := ast.Unparen(condition).(*ast.BinaryExpr)
	if !ok || (binary.Op != token.NEQ && binary.Op != token.EQL) {
		return false
	}
	info := checker.pass.TypesInfo
	for _, pair := range [][2]ast.Expr{{binary.X, binary.Y}, {binary.Y, binary.X}} {
		if !info.Types[pair[1]].IsNil() {
			continue
		}
		valueType := info.TypeOf(pair[0])
		if valueType != nil && types.Identical(valueType, types.Universe.Lookup("error").Type()) {
			return true
		}
	}
	return false
}

func (checker assertionChecker) onlySkips(body *ast.BlockStmt) bool {
	if len(body.List) == 0 {
		return false
	}
	for _, statement := range body.List {
		expression, ok := statement.(*ast.ExprStmt)
		if !ok {
			return false
		}
		call, ok := expression.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		method := checker.testingMethod(call)
		if method == nil || !skippingTestingMethods[method.Name()] {
			return false
		}
	}
	return true
}

func (checker assertionChecker) isFailingCall(call *ast.CallExpr) bool {
	if checker.pass.TypesInfo.Types[call.Fun].IsType() {
		return false
	}
	if method := checker.testingMethod(call); method != nil {
		if failingTestingMethods[method.Name()] {
			return true
		}
	}
	if checker.isTestRunCall(call) {
		return checker.subtestLiteral(call) == nil
	}
	return checker.passesTestingValue(call)
}

func (checker assertionChecker) passesTestingValue(call *ast.CallExpr) bool {
	for _, argument := range call.Args {
		argumentType := checker.pass.TypesInfo.TypeOf(argument)
		if argumentType == nil {
			continue
		}
		if types.Implements(argumentType, checker.tbInterface) {
			return true
		}
	}
	return false
}
