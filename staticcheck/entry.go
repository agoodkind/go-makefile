package staticcheck

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// CommandEntryAnalyzer requires command packages to delegate implementation.
var CommandEntryAnalyzer = &analysis.Analyzer{
	Name: "commandentry",
	Doc:  "requires cmd entry points to delegate execution to an imported package",
	Run:  runCommandEntry,
}

const (
	entryErrorPredicateArguments = 2
	entryWrappingArguments       = 2
)

func runCommandEntry(pass *analysis.Pass) (any, error) {
	packagePath := pass.Pkg.Path()
	if pass.Module != nil {
		packagePath = strings.TrimPrefix(packagePath, pass.Module.Path)
	}

	if !strings.Contains("/"+packagePath+"/", "/cmd/") {
		return nil, nil
	}

	grammar := newCommandGrammar(pass)

	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedTestMainFile(file, path) {
			continue
		}

		counts := make(map[string]int)

		for _, declaration := range file.Decls {
			if general, ok := declaration.(*ast.GenDecl); ok && grammar.declaration(general) {
				continue
			}

			if function, ok := declaration.(*ast.FuncDecl); ok && grammar.function(function) {
				continue
			}

			if grammar.importError != nil {
				return nil, grammar.importError
			}

			identity, err := nodeFingerprint(declaration)
			if err != nil {
				return nil, err
			}

			counts[identity]++
			reportAtf(
				pass,
				file,
				declaration.Pos(),
				"commandentry: %s contains implementation in cmd; delegate to an importable package [id=%s-%d]",
				declarationName(declaration),
				identity,
				counts[identity],
			)
		}
	}

	return nil, nil
}

type commandGrammar struct {
	pass        *analysis.Pass
	functions   map[*types.Func]*ast.FuncDecl
	checking    map[entryFunctionKey]bool
	checked     map[entryFunctionKey]bool
	valid       map[entryFunctionKey]bool
	imports     map[*types.Package]bool
	importError error
}

type entryFunctionKey struct {
	function     *ast.FuncDecl
	statusBranch bool
}

func newCommandGrammar(pass *analysis.Pass) *commandGrammar {
	grammar := &commandGrammar{
		pass:      pass,
		functions: make(map[*types.Func]*ast.FuncDecl),
		checking:  make(map[entryFunctionKey]bool),
		checked:   make(map[entryFunctionKey]bool),
		valid:     make(map[entryFunctionKey]bool),
		imports:   make(map[*types.Package]bool),
	}

	for _, file := range pass.Files {
		if isTestFile(fileName(pass, file.Pos())) {
			continue
		}

		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				if object, ok := pass.TypesInfo.Defs[function.Name].(*types.Func); ok {
					grammar.functions[object] = function
				}
			}
		}
	}

	return grammar
}

func (grammar *commandGrammar) declaration(declaration *ast.GenDecl) bool {
	if declaration.Tok == token.IMPORT {
		return true
	}

	if declaration.Tok != token.CONST {
		return false
	}

	for _, specification := range declaration.Specs {
		values, ok := specification.(*ast.ValueSpec)
		if !ok {
			return false
		}

		for _, name := range values.Names {
			object := grammar.pass.TypesInfo.Defs[name]
			if object == nil || !entryInteger(object.Type()) {
				return false
			}
		}
	}

	return true
}

func (grammar *commandGrammar) function(function *ast.FuncDecl) bool {
	return grammar.functionInBranch(function, false)
}

func (grammar *commandGrammar) functionInBranch(function *ast.FuncDecl, statusBranch bool) bool {
	key := entryFunctionKey{function: function, statusBranch: statusBranch}
	if function == nil || function.Recv != nil || function.Body == nil || grammar.checking[key] {
		return false
	}

	if grammar.checked[key] {
		return grammar.valid[key]
	}

	grammar.checking[key] = true
	body := entryBody{
		grammar:      grammar,
		values:       make(map[types.Object]bool),
		statuses:     make(map[types.Object]bool),
		literals:     make(map[types.Object]bool),
		used:         make(map[types.Object]bool),
		origins:      make(map[types.Object][]types.Object),
		statusBranch: statusBranch,
	}

	for _, parameter := range function.Type.Params.List {
		for _, name := range parameter.Names {
			object := grammar.pass.TypesInfo.Defs[name]
			body.values[object] = true
		}
	}

	valid := body.block(function.Body.List, false) && body.handled && body.usedLiterals()
	grammar.checking[key] = false
	grammar.checked[key] = true
	grammar.valid[key] = valid

	return valid
}

type entryBody struct {
	grammar      *commandGrammar
	values       map[types.Object]bool
	statuses     map[types.Object]bool
	handled      bool
	statusBranch bool
	literals     map[types.Object]bool
	used         map[types.Object]bool
	origins      map[types.Object][]types.Object
}

func (body *entryBody) block(statements []ast.Stmt, handling bool) bool {
	for _, statement := range statements {
		if !body.statement(statement, handling) {
			return false
		}
	}

	return true
}

func (body *entryBody) statement(statement ast.Stmt, handling bool) bool {
	switch typed := statement.(type) {
	case *ast.ExprStmt:
		_, ok := typed.X.(*ast.CallExpr)

		return ok && body.expression(typed.X)
	case *ast.ReturnStmt:
		for _, result := range typed.Results {
			valueType := body.grammar.pass.TypesInfo.TypeOf(result)
			if entryInteger(valueType) || entryError(valueType) {
				body.useValues([]ast.Expr{result})
			}
		}

		return body.expressions(typed.Results)
	case *ast.AssignStmt:
		return body.assignment(typed, handling)
	case *ast.IfStmt:
		if typed.Init != nil && !body.statement(typed.Init, false) {
			return false
		}

		valid, statusBranch := body.condition(typed.Cond)
		if !valid {
			return false
		}

		previous := body.statusBranch
		body.statusBranch = previous || statusBranch
		defer func() { body.statusBranch = previous }()
		if !body.block(typed.Body.List, true) {
			return false
		}

		return typed.Else == nil || body.statement(typed.Else, true)
	case *ast.BlockStmt:
		return body.block(typed.List, handling)
	case *ast.SwitchStmt:
		return body.statusSwitch(typed)
	}

	return false
}

func (body *entryBody) assignment(statement *ast.AssignStmt, handling bool) bool {
	if statement.Tok != token.DEFINE && statement.Tok != token.ASSIGN {
		return false
	}

	if !body.expressions(statement.Rhs) {
		return false
	}

	delegated := false

	for _, right := range statement.Rhs {
		if _, ok := ast.Unparen(right).(*ast.CallExpr); ok {
			delegated = true
		}
	}

	for _, left := range statement.Lhs {
		name, ok := left.(*ast.Ident)
		if !ok {
			return false
		}

		if name.Name == "_" {
			continue
		}

		object := body.grammar.pass.TypesInfo.ObjectOf(name)
		if !body.values[object] && literalEntryValues(body.grammar.pass.TypesInfo, statement.Rhs) {
			body.literals[object] = true
		}

		body.recordOrigins(object, statement.Rhs)
		body.values[object] = true
		if object != nil && entryInteger(object.Type()) && (delegated || handling) {
			body.statuses[object] = true
		}
	}

	return true
}

func (body *entryBody) condition(expression ast.Expr) (bool, bool) {
	if unary, ok := ast.Unparen(expression).(*ast.UnaryExpr); ok && unary.Op == token.NOT {
		return body.condition(unary.X)
	}

	if call, ok := ast.Unparen(expression).(*ast.CallExpr); ok {
		return body.errorPredicate(call), false
	}

	binary, ok := ast.Unparen(expression).(*ast.BinaryExpr)
	if !ok {
		return false, false
	}

	if binary.Op == token.LAND || binary.Op == token.LOR {
		first, firstStatus := body.condition(binary.X)
		second, secondStatus := body.condition(binary.Y)

		return first && second, firstStatus || secondStatus
	}

	if binary.Op == token.EQL || binary.Op == token.NEQ {
		if body.errorComparison(binary.X, binary.Y) || body.errorComparison(binary.Y, binary.X) {
			return true, false
		}
	}

	if !slices.Contains([]token.Token{token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ}, binary.Op) {
		return false, false
	}

	info := body.grammar.pass.TypesInfo
	if info.Types[binary.X].Value != nil && info.Types[binary.Y].Value != nil {
		return false, false
	}

	return body.status(binary.X) && body.status(binary.Y), true
}

func (body *entryBody) errorComparison(value, nilValue ast.Expr) bool {
	info := body.grammar.pass.TypesInfo
	body.useValues([]ast.Expr{value})

	return info.Types[nilValue].IsNil() && entryError(info.TypeOf(value)) && body.expression(value)
}

func (body *entryBody) status(expression ast.Expr) bool {
	info := body.grammar.pass.TypesInfo
	if !entryInteger(info.TypeOf(expression)) {
		return false
	}

	if info.Types[expression].Value != nil {
		return true
	}

	if name, ok := ast.Unparen(expression).(*ast.Ident); ok {
		return body.statuses[info.ObjectOf(name)]
	}

	if _, ok := ast.Unparen(expression).(*ast.CallExpr); ok {
		return body.expression(expression)
	}

	return false
}

func (body *entryBody) statusSwitch(statement *ast.SwitchStmt) bool {
	if statement.Init != nil && !body.statement(statement.Init, false) {
		return false
	}

	if statement.Tag == nil || !body.status(statement.Tag) {
		return false
	}

	previous := body.statusBranch
	body.statusBranch = true
	defer func() { body.statusBranch = previous }()

	for _, statement := range statement.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok {
			return false
		}

		for _, value := range clause.List {
			if !body.status(value) {
				return false
			}
		}

		if !body.block(clause.Body, true) {
			return false
		}
	}

	return true
}

func (body *entryBody) expressions(expressions []ast.Expr) bool {
	for _, expression := range expressions {
		if !body.expression(expression) {
			return false
		}
	}

	return true
}

func (body *entryBody) expression(expression ast.Expr) bool {
	info := body.grammar.pass.TypesInfo
	if info.Types[expression].Value != nil {
		return true
	}

	switch typed := ast.Unparen(expression).(type) {
	case *ast.Ident:
		object := info.ObjectOf(typed)
		switch object.(type) {
		case *types.Nil, *types.Const, *types.PkgName, *types.Func:
			return true
		}

		if body.values[object] && entryError(info.TypeOf(typed)) {
			body.handled = true
		}

		if variable, ok := object.(*types.Var); ok && variable.Parent() == body.grammar.pass.Pkg.Scope() {
			return true
		}

		return body.values[object]
	case *ast.SelectorExpr:
		return body.expression(typed.X)
	case *ast.SliceExpr:
		return body.expression(typed.X) && body.index(typed.Low) && body.index(typed.High) && body.index(typed.Max)
	case *ast.IndexExpr:
		return body.expression(typed.X) && body.index(typed.Index)
	case *ast.CallExpr:
		return body.call(typed)
	case *ast.FuncLit:
		return body.importedCallback(typed)
	}

	return false
}

func (body *entryBody) importedCallback(literal *ast.FuncLit) bool {
	if len(literal.Body.List) != 1 {
		return false
	}

	var expression ast.Expr
	switch statement := literal.Body.List[0].(type) {
	case *ast.ReturnStmt:
		if len(statement.Results) != 1 {
			return false
		}

		expression = statement.Results[0]
	case *ast.ExprStmt:
		expression = statement.X
	}

	call, callable := expression.(*ast.CallExpr)
	if !callable {
		return false
	}

	function, resolved := typeutil.Callee(body.grammar.pass.TypesInfo, call).(*types.Func)
	if !resolved || function.Pkg() == nil || function.Pkg() == body.grammar.pass.Pkg {
		return false
	}

	if !body.grammar.applicationFunction(function) {
		return false
	}

	declaration := &ast.FuncDecl{Type: literal.Type, Body: literal.Body}

	return body.grammar.functionInBranch(declaration, body.statusBranch)
}

func (body *entryBody) index(expression ast.Expr) bool {
	return expression == nil || body.grammar.pass.TypesInfo.Types[expression].Value != nil
}

func (body *entryBody) call(call *ast.CallExpr) bool {
	pass := body.grammar.pass
	if pass.TypesInfo.Types[call.Fun].IsType() {
		return body.statusConversion(call)
	}

	function, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok || !body.calleeReceiver(call.Fun) || !body.expressions(call.Args) {
		return false
	}

	if function.Pkg() == nil {
		selector, ok := call.Fun.(*ast.SelectorExpr)
		errorMethod := function.Name() == selectorError && len(call.Args) == 0

		return ok && errorMethod && entryError(pass.TypesInfo.TypeOf(selector.X))
	}

	path := function.Pkg().Path()
	if path == "os" && function.Name() == "Exit" {
		body.useValues(call.Args)
		body.handled = true

		return len(call.Args) == 1 && entryInteger(pass.TypesInfo.TypeOf(call.Args[0]))
	}

	if path == "log/slog" {
		body.useValues(call.Args)

		return body.loggingCall(function.Name())
	}

	if path == fmtPackagePath && function.Name() == selectorErrorf {
		body.useValues(call.Args)

		return body.wrappedError(call)
	}

	if path == errorsPackagePath && function.Name() == "Join" {
		body.useValues(call.Args)

		for _, argument := range call.Args {
			if !entryError(pass.TypesInfo.TypeOf(argument)) {
				return false
			}
		}

		body.handled = true

		return len(call.Args) > 0
	}

	if path == errorsPackagePath && function.Name() == "Is" {
		return body.errorPredicate(call)
	}

	if function.Pkg() == pass.Pkg {
		if !body.grammar.functionInBranch(body.grammar.functions[function.Origin()], body.statusBranch) {
			return false
		}
	} else if body.statusBranch || !body.grammar.applicationFunction(function) {
		return false
	}

	body.handled = true

	return true
}

func (body *entryBody) errorPredicate(call *ast.CallExpr) bool {
	pass := body.grammar.pass
	function, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok || function.Pkg() == nil {
		return false
	}

	errorFunction := function.Pkg().Path() == errorsPackagePath && function.Name() == "Is"
	if !errorFunction || len(call.Args) != entryErrorPredicateArguments {
		return false
	}

	if !entryError(pass.TypesInfo.TypeOf(call.Args[0])) {
		return false
	}

	if !entryError(pass.TypesInfo.TypeOf(call.Args[1])) && !pass.TypesInfo.Types[call.Args[1]].IsNil() {
		return false
	}

	if !body.expressions(call.Args) {
		return false
	}

	body.useValues(call.Args)
	body.handled = true

	return true
}

func literalEntryValues(info *types.Info, expressions []ast.Expr) bool {
	for _, expression := range expressions {
		value := info.Types[expression]
		if value.Value != nil || value.IsNil() {
			return true
		}
	}

	return false
}

func (body *entryBody) recordOrigins(object types.Object, expressions []ast.Expr) {
	for _, expression := range expressions {
		ast.Inspect(expression, func(node ast.Node) bool {
			if name, ok := node.(*ast.Ident); ok {
				origin := body.grammar.pass.TypesInfo.ObjectOf(name)
				body.origins[object] = append(body.origins[object], origin)
			}

			return true
		})
	}
}

func (body *entryBody) useValues(expressions []ast.Expr) {
	for _, expression := range expressions {
		ast.Inspect(expression, func(node ast.Node) bool {
			if name, ok := node.(*ast.Ident); ok {
				body.useObject(body.grammar.pass.TypesInfo.ObjectOf(name))
			}

			return true
		})
	}
}

func (body *entryBody) useObject(object types.Object) {
	if body.used[object] {
		return
	}

	body.used[object] = true
	for _, origin := range body.origins[object] {
		body.useObject(origin)
	}
}

func (body *entryBody) usedLiterals() bool {
	for object := range body.literals {
		if !body.used[object] {
			return false
		}
	}

	return true
}

func (body *entryBody) calleeReceiver(expression ast.Expr) bool {
	switch typed := ast.Unparen(expression).(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		return body.expression(typed.X)
	case *ast.IndexExpr:
		return body.calleeReceiver(typed.X)
	case *ast.IndexListExpr:
		return body.calleeReceiver(typed.X)
	}

	return false
}

func (body *entryBody) statusConversion(call *ast.CallExpr) bool {
	if len(call.Args) != 1 || !entryInteger(body.grammar.pass.TypesInfo.TypeOf(call)) {
		return false
	}

	return body.status(call.Args[0])
}

func (body *entryBody) loggingCall(name string) bool {
	switch name {
	case selectorDebug,
		selectorInfo,
		selectorWarn,
		selectorError,
		selectorLog,
		selectorLogAttrs,
		selectorDebugContext,
		selectorInfoContext,
		selectorWarnContext,
		selectorErrorContext:
		body.handled = true

		return true
	case "Any", "Bool", "Duration", "Float64", "Group", "GroupAttrs", "Int", "Int64", "String", "Time", "Uint64":
		return true
	}

	return false
}

func (body *entryBody) wrappedError(call *ast.CallExpr) bool {
	if len(call.Args) < entryWrappingArguments {
		return false
	}

	for _, argument := range call.Args[1:] {
		if entryError(body.grammar.pass.TypesInfo.TypeOf(argument)) {
			body.handled = true

			return true
		}
	}

	return false
}

func (grammar *commandGrammar) applicationFunction(function *types.Func) bool {
	owner := function.Pkg()
	if application, found := grammar.imports[owner]; found {
		return application
	}

	path := grammar.pass.Fset.PositionFor(function.Pos(), false).Filename
	metadata, err := build.Default.Import(owner.Path(), filepath.Dir(path), build.FindOnly)
	if err != nil {
		grammar.importError = fmt.Errorf("inspect command import %q: %w", owner.Path(), err)

		return false
	}

	application := !metadata.Goroot
	grammar.imports[owner] = application

	return application
}

func entryInteger(value types.Type) bool {
	if value == nil {
		return false
	}

	basic, ok := value.Underlying().(*types.Basic)

	return ok && basic.Info()&types.IsInteger != 0
}

func entryError(value types.Type) bool {
	return value != nil && types.AssignableTo(value, types.Universe.Lookup("error").Type())
}
