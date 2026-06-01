package staticcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// NoTildePathLiteralAnalyzer flags string literals that equal "~" or
// start with "~/". The Go standard library never expands tilde in path
// arguments ([os.Open], [os.MkdirAll], filepath.*, [exec.Command], and so
// on all treat "~" as a literal directory name), so any such literal is a
// latent bug that creates a literal "~" directory at runtime instead of
// resolving to the user's home directory. Use [os.UserHomeDir] and
// [filepath.Join] to build the path explicitly.
var NoTildePathLiteralAnalyzer = &analysis.Analyzer{
	Name: "no_tilde_path_literal",
	Doc:  "rejects string literals beginning with ~/ or equal to ~; use os.UserHomeDir() + filepath.Join instead",
	Run:  runNoTildePathLiteral,
}

func runNoTildePathLiteral(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok {
				return true
			}
			value, ok := stringLiteral(lit)
			if !ok {
				return true
			}
			if !looksLikeTildeHomePath(value) {
				return true
			}
			reportAtf(pass, file, lit.Pos(), "hardcoded home directory %q; use os.UserHomeDir() or filepath.Join(homeDir, ...) since Go does not expand ~", value)
			return true
		})
	}
	return nil, nil
}

func looksLikeTildeHomePath(value string) bool {
	if value == "~" {
		return true
	}
	return strings.HasPrefix(value, "~/")
}
