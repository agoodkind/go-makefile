package staticcheck

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

const (
	testDataDirName    = "testdata"
	embedDirectiveText = "//go:embed"
	osPackagePath      = "os"
	pathPackagePath    = "path"
	filepathPackage    = "path/filepath"
	joinFunctionName   = "Join"
	goParserPackage    = "go/parser"
	parentDirElement   = ".."
)

var (
	osFileOpenFunctions = map[string]bool{
		"ReadFile": true,
		"Open":     true,
		"OpenFile": true,
	}
	systemPathPrefixes = []string{"/dev/", "/proc/", "/sys/", "/etc/"}
)

// TestSourceFileAnalyzer flags a _test.go file that reads a repository file as
// text input. An assertion on the text of a source, template, or configuration
// file passes while the behavior that uses the file is broken. The required
// form runs the code that uses the file and asserts on its outcome.
//
// A read is exempt when the test passes the content to production code or
// writes it to another file. A path with an element named testdata is exempt
// in both clauses. A path with a non-constant part and no parent directory
// element is not reported: the analyzer cannot determine the file.
var TestSourceFileAnalyzer = &analysis.Analyzer{
	Name: "testsourcefile",
	Doc:  "rejects a test that reads a repository file by constant path or embeds it outside testdata",
	Run:  runTestSourceFile,
}

func runTestSourceFile(pass *analysis.Pass) (any, error) {
	for _, file := range analyzableTestFiles(pass) {
		reportEmbedDirectives(pass, file)
		for _, declaration := range file.Decls {
			var body *ast.BlockStmt
			if function, ok := declaration.(*ast.FuncDecl); ok {
				body = function.Body
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if ok {
					reportConstantFileRead(pass, file, body, call)
				}
				return true
			})
		}
	}
	return nil, nil
}

// The content is the non-error result
// of the read and each local variable assigned from an expression that uses
// it. Production code receives the content when a call to a function declared
// in a non-test file of the same module has it as an argument, or when the
// test writes it to another file with os.WriteFile. Such a file is test input
// and not text under assertion.
func readIsProductionInput(pass *analysis.Pass, body *ast.BlockStmt, read *ast.CallExpr) bool {
	if body == nil {
		return false
	}
	tainted := make(map[types.Object]bool)
	uses := func(expr ast.Node) bool {
		found := false
		ast.Inspect(expr, func(node ast.Node) bool {
			if node == read {
				found = true
			}
			if ident, ok := node.(*ast.Ident); ok && tainted[pass.TypesInfo.ObjectOf(ident)] {
				found = true
			}
			return !found
		})
		return found
	}
	for changed := true; changed; {
		changed = false
		ast.Inspect(body, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			fromRead := false
			for _, right := range assign.Rhs {
				if uses(right) {
					fromRead = true
				}
			}
			if !fromRead {
				return true
			}
			for _, left := range assign.Lhs {
				ident, ok := left.(*ast.Ident)
				if !ok || ident.Name == "_" {
					continue
				}
				obj := pass.TypesInfo.ObjectOf(ident)
				if obj == nil || tainted[obj] || isErrorType(obj.Type()) {
					continue
				}
				tainted[obj] = true
				changed = true
			}
			return true
		})
	}
	passed := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || call == read || passed {
			return !passed
		}
		if !receivesInput(pass, call) {
			return true
		}
		for _, argument := range call.Args {
			if uses(argument) {
				passed = true
			}
		}
		return !passed
	})
	return passed
}

func receivesInput(pass *analysis.Pass, call *ast.CallExpr) bool {
	function, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok || function.Pkg() == nil {
		return false
	}
	if function.Pkg().Path() == osPackagePath && function.Name() == "WriteFile" {
		return true
	}
	return sameModulePackage(pass, function.Pkg().Path()) && !isTestFile(fileName(pass, function.Pos()))
}

func reportConstantFileRead(pass *analysis.Pass, file *ast.File, body *ast.BlockStmt, call *ast.CallExpr) {
	if isSourceParserCall(pass, call) {
		reportAtf(
			pass, file, call.Pos(),
			"This test parses Go source with go/parser. A test must assert on behavior and not on the text of a source file. Run the code and assert on its outcome.",
		)
		return
	}
	if !isOSFileOpenCall(pass, call) || len(call.Args) == 0 {
		return
	}
	if readIsProductionInput(pass, body, call) {
		return
	}
	if joinHasParentElement(pass, call.Args[0]) {
		reportAtf(
			pass, file, call.Pos(),
			"This test reads a file through a parent directory path. A test must assert on behavior and not on the text of a source, template, or configuration file. Run the code that uses the file and assert on its outcome, or move the test input under testdata.",
		)
		return
	}
	filePath, ok := constantPathArgument(pass, call.Args[0])
	if !ok || isAllowedConstantPath(filePath) {
		return
	}
	reportAtf(
		pass, file, call.Pos(),
		"This test reads the file at the constant path %q. A test must assert on behavior and not on the text of a source, template, or configuration file. Run the code that uses the file and assert on its outcome, or move the test input under testdata.",
		filePath,
	)
}

func isSourceParserCall(pass *analysis.Pass, call *ast.CallExpr) bool {
	function, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok || function.Pkg() == nil || function.Pkg().Path() != goParserPackage {
		return false
	}
	return function.Name() == "ParseFile" || function.Name() == "ParseDir"
}

// A path with only constant parts is handled by constantPathArgument.
func joinHasParentElement(pass *analysis.Pass, expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || !isPathJoinCall(pass, call) {
		return false
	}
	hasParent := false
	hasVariable := false
	for _, argument := range call.Args {
		value, isConstant := constantString(pass, argument)
		if !isConstant {
			hasVariable = true
			continue
		}
		if hasTestDataElement(value) {
			return false
		}
		for _, element := range strings.Split(filepath.ToSlash(value), "/") {
			if element == parentDirElement {
				hasParent = true
			}
		}
	}
	return hasParent && hasVariable
}

func isOSFileOpenCall(pass *analysis.Pass, call *ast.CallExpr) bool {
	function, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok || function.Pkg() == nil || function.Pkg().Path() != osPackagePath {
		return false
	}
	signature, ok := function.Type().(*types.Signature)
	if !ok || signature.Recv() != nil {
		return false
	}
	return osFileOpenFunctions[function.Name()]
}

func constantPathArgument(pass *analysis.Pass, expr ast.Expr) (string, bool) {
	if value, ok := constantString(pass, expr); ok {
		return value, true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || call.Ellipsis.IsValid() || !isPathJoinCall(pass, call) {
		return "", false
	}
	elements := make([]string, 0, len(call.Args))
	for _, argument := range call.Args {
		value, ok := constantString(pass, argument)
		if !ok {
			return "", false
		}
		elements = append(elements, value)
	}
	return filepath.ToSlash(filepath.Join(elements...)), true
}

func constantString(pass *analysis.Pass, expr ast.Expr) (string, bool) {
	typeAndValue, ok := pass.TypesInfo.Types[expr]
	if !ok || typeAndValue.Value == nil || typeAndValue.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(typeAndValue.Value), true
}

func isPathJoinCall(pass *analysis.Pass, call *ast.CallExpr) bool {
	function, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok || function.Pkg() == nil || function.Name() != joinFunctionName {
		return false
	}
	packagePath := function.Pkg().Path()
	return packagePath == filepathPackage || packagePath == pathPackagePath
}

func isAllowedConstantPath(filePath string) bool {
	if hasTestDataElement(filePath) {
		return true
	}
	for _, prefix := range systemPathPrefixes {
		if strings.HasPrefix(filePath, prefix) {
			return true
		}
	}
	return false
}

func hasTestDataElement(filePath string) bool {
	cleaned := path.Clean(filepath.ToSlash(filePath))
	for _, element := range strings.Split(cleaned, "/") {
		if element == testDataDirName {
			return true
		}
	}
	return false
}

func reportEmbedDirectives(pass *analysis.Pass, file *ast.File) {
	for _, declaration := range file.Decls {
		genDecl, ok := declaration.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			reportEmbedGroup(pass, file, genDecl.Doc, valueSpec)
			reportEmbedGroup(pass, file, valueSpec.Doc, valueSpec)
		}
	}
}

func reportEmbedGroup(
	pass *analysis.Pass,
	file *ast.File,
	group *ast.CommentGroup,
	spec *ast.ValueSpec,
) {
	if group == nil || len(spec.Names) == 0 {
		return
	}
	for _, comment := range group.List {
		pattern, found := outsideTestDataPattern(comment.Text)
		if !found {
			continue
		}
		reportAtf(
			pass, file, spec.Names[0].Pos(),
			"This test embeds the file pattern %q. A test must assert on behavior and not on the text of a source, template, or configuration file. Run the code that uses the file and assert on its outcome, or move the test input under testdata.",
			pattern,
		)
	}
}

func outsideTestDataPattern(commentText string) (string, bool) {
	rest, ok := strings.CutPrefix(commentText, embedDirectiveText)
	if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	for _, pattern := range embedPatterns(rest) {
		if !hasTestDataElement(pattern) {
			return pattern, true
		}
	}
	return "", false
}

// A quoted pattern may contain whitespace.
func embedPatterns(arguments string) []string {
	var patterns []string
	remaining := strings.TrimSpace(arguments)
	for remaining != "" {
		var pattern string
		pattern, remaining = nextEmbedPattern(remaining)
		patterns = append(patterns, pattern)
		remaining = strings.TrimSpace(remaining)
	}
	return patterns
}

func nextEmbedPattern(arguments string) (string, string) {
	quote := arguments[0]
	if quote == '"' || quote == '`' {
		quoted, err := strconv.QuotedPrefix(arguments)
		if err == nil {
			unquoted, unquoteErr := strconv.Unquote(quoted)
			if unquoteErr == nil {
				return unquoted, arguments[len(quoted):]
			}
		}
	}
	end := strings.IndexAny(arguments, " \t")
	if end < 0 {
		return arguments, ""
	}
	return arguments[:end], arguments[end:]
}
