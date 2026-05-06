package staticcheck

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

func fileName(pass *analysis.Pass, pos token.Pos) string {
	return filepath.ToSlash(pass.Fset.Position(pos).Filename)
}

func reportAtf(pass *analysis.Pass, file *ast.File, preferred token.Pos, format string, args ...any) {
	position := diagnosticPos(pass, file, preferred)
	if !diagnosticPathIsActionable(pass, position) {
		return
	}
	pass.Report(analysis.Diagnostic{
		Pos:     position,
		Message: fmt.Sprintf(format, args...),
	})
}

func diagnosticPos(pass *analysis.Pass, file *ast.File, preferred token.Pos) token.Pos {
	if preferred.IsValid() && diagnosticPathIsActionable(pass, preferred) {
		return preferred
	}
	if file != nil && file.Package.IsValid() {
		return file.Package
	}
	if file != nil && file.Pos().IsValid() {
		return file.Pos()
	}
	return preferred
}

func diagnosticPathIsActionable(pass *analysis.Pass, pos token.Pos) bool {
	path := fileName(pass, pos)
	return path != "" && !isGoBuildCacheDescriptorPath(path)
}

func isGoBuildCacheDescriptorPath(path string) bool {
	slashPath := filepath.ToSlash(filepath.Clean(path))
	if !strings.HasSuffix(filepath.Base(slashPath), "-d") {
		return false
	}
	if gocache := os.Getenv("GOCACHE"); gocache != "" {
		if pathIsWithin(slashPath, filepath.ToSlash(filepath.Clean(gocache))) {
			return true
		}
	}
	if userCacheDir, err := os.UserCacheDir(); err == nil {
		goBuildCache := filepath.ToSlash(filepath.Join(userCacheDir, "go-build"))
		if pathIsWithin(slashPath, goBuildCache) {
			return true
		}
	}
	return strings.Contains(slashPath, "/go-build/")
}

func pathIsWithin(path string, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
}

func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

func shouldAnalyzeFile(pass *analysis.Pass, file *ast.File) bool {
	path := fileName(pass, file.Pos())
	return !isTestFile(path) &&
		!isGeneratedFile(file, path) &&
		!isGeneratedTestMainFile(file, path) &&
		!isProtobufGeneratedPath(path) &&
		!isStaticcheckPath(path)
}

// isGeneratedFile returns true when the file is conventionally generated
// code that should be exempted from analysis. The check requires BOTH a
// recognised filename pattern AND the standard header marker. Either one
// alone is insufficient, because an LLM could add a fake header to a
// production file to launder analyzer findings, and the filename
// convention alone is meaningless without the canonical header.
//
// Recognised generator patterns (filename ends with one of):
//   - .pb.go               (protobuf via protoc-gen-go)
//   - .pb.gw.go            (grpc-gateway)
//   - .pb.validate.go      (protoc-gen-validate)
//   - _gen.go, .gen.go     (go generate convention)
//   - _generated.go        (mockery and similar)
//   - .twirp.go, _twirp.go (twirp)
//   - .connect.go          (connectrpc)
//   - .yarpc.go            (yarpc)
//   - bindata.go           (go-bindata, embedded asset)
//
// In addition, the file must contain the canonical `Code generated` or
// `DO NOT EDIT` header marker.
func isGeneratedFile(file *ast.File, path string) bool {
	if file == nil {
		return false
	}
	if !generatedFilenameLooksConventional(path) && !isGoBuildCacheDescriptorPath(path) {
		return false
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.ToLower(comment.Text)
			if strings.Contains(text, "code generated") || strings.Contains(text, "do not edit") {
				return true
			}
		}
	}
	return false
}

func generatedFilenameLooksConventional(path string) bool {
	if path == "" {
		return false
	}
	base := filepath.Base(path)
	suffixes := []string{
		".pb.go",
		".pb.gw.go",
		".pb.validate.go",
		"_gen.go",
		".gen.go",
		"_generated.go",
		".twirp.go",
		"_twirp.go",
		".connect.go",
		".yarpc.go",
	}
	for _, s := range suffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	return base == "bindata.go"
}

func isGeneratedTestMainFile(file *ast.File, path string) bool {
	if file == nil || file.Name == nil || file.Name.Name != "main" {
		return false
	}
	cacheDescriptorPath := isGoBuildCacheDescriptorPath(path)
	if !cacheDescriptorPath && filepath.Base(path) != "_testmain.go" {
		return false
	}
	if !cacheDescriptorPath && !hasGeneratedComment(file) {
		return false
	}
	return importsPath(file, "testing/internal/testdeps") &&
		assignsSelector(file, "testdeps", "ModulePath") &&
		assignsSelector(file, "testdeps", "ImportPath") &&
		hasFunction(file, "main")
}

func hasGeneratedComment(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.ToLower(comment.Text)
			if strings.Contains(text, "code generated") || strings.Contains(text, "do not edit") {
				return true
			}
		}
	}
	return false
}

func importsPath(file *ast.File, importPath string) bool {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if path == importPath {
			return true
		}
	}
	return false
}

func assignsSelector(file *ast.File, receiver string, name string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if found {
			return false
		}
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, expr := range assignment.Lhs {
			selector, ok := expr.(*ast.SelectorExpr)
			if !ok || selector.Sel == nil || selector.Sel.Name != name {
				continue
			}
			ident, ok := selector.X.(*ast.Ident)
			if ok && ident.Name == receiver {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func hasFunction(file *ast.File, name string) bool {
	for _, declaration := range file.Decls {
		functionDeclaration, ok := declaration.(*ast.FuncDecl)
		if !ok || functionDeclaration.Name == nil {
			continue
		}
		if functionDeclaration.Name.Name == name {
			return true
		}
	}
	return false
}

func packagePath(pass *analysis.Pass) string {
	if pass.Pkg == nil {
		return ""
	}
	return pass.Pkg.Path()
}

func isStaticcheckPackage(pass *analysis.Pass) bool {
	return strings.HasSuffix(packagePath(pass), "/internal/staticcheck")
}

// isStaticcheckPath returns true if path lives in this analyzer's own
// source tree, used by analyzer rules to skip self-analysis. Matches both
// the standalone staticcheck module layout and the original
// internal/staticcheck layout for callers that vendored the source.
func isStaticcheckPath(path string) bool {
	return strings.Contains(path, "/staticcheck/") || strings.Contains(path, "/internal/staticcheck/")
}

func isProtobufGeneratedPath(path string) bool {
	return strings.HasSuffix(path, ".pb.go") || strings.Contains(path, "/api/")
}

func selectorName(expr ast.Expr) (receiver string, name string, ok bool) {
	call, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	name = call.Sel.Name
	switch recv := call.X.(type) {
	case *ast.Ident:
		receiver = recv.Name
	case *ast.SelectorExpr:
		_, recvName, recvOK := selectorName(recv)
		if recvOK {
			receiver = recvName
		}
	}
	return receiver, name, true
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value := strings.Trim(lit.Value, "`\"")
	return value, true
}

func inspectFunc(fn *ast.FuncDecl, visit func(ast.Node) bool) {
	if fn == nil || fn.Body == nil {
		return
	}
	ast.Inspect(fn.Body, visit)
}
