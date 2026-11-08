package staticcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"os"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

type analysisSource struct {
	path     string
	file     *ast.File
	contents []byte
}

func originalSources(pass *analysis.Pass) ([]analysisSource, error) {
	seen := make(map[string]bool)

	sources := make([]analysisSource, 0, len(pass.Files))
	for _, file := range pass.Files {
		physical := pass.Fset.PositionFor(file.Pos(), false).Filename
		path := physical
		if isGoBuildCacheDescriptorPath(physical) && hasGeneratedComment(file) {
			path = fileName(pass, file.Name.Pos())
		}

		if seen[path] || !strings.HasSuffix(path, ".go") || isGeneratedTestMainFile(file, path) {
			continue
		}

		seen[path] = true
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read analyzer source: %w", err)
		}

		// Cgo supplies a generated syntax tree with positions mapped to the source.
		// Parse that source before using its byte offsets.
		if physical != path {
			file, err = parser.ParseFile(pass.Fset, path, contents, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("parse original analyzer source: %w", err)
			}

			// Register the original source with the driver's checked file reader.
			pass.Files = append(slices.Clone(pass.Files), file)
		}

		sources = append(sources, analysisSource{path: path, file: file, contents: contents})
	}

	return sources, nil
}
