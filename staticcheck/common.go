package staticcheck

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
)

func fileName(pass *analysis.Pass, pos token.Pos) string {
	return filepath.ToSlash(pass.Fset.Position(pos).Filename)
}

func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
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
	if !generatedFilenameLooksConventional(path) {
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
