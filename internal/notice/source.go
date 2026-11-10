package notice

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
)

const findingLocationParts = 3

// Source stores parsed Go syntax for one historical capture.
type Source struct {
	file  *ast.File
	files *token.FileSet
}

// SourceIdentity identifies one finding and the Go code that produced it.
//
// Formatting preserves the identity. Code and comment changes alter it.
// Duplicate reports include every overlapping declaration in both blocks.
// File-length reports identify the whole file, including reports at EOF.
//
// For example:
//
//	identity, err := SourceIdentity(make(map[string]Source), finding, checkout)
func SourceIdentity(sources map[string]Source, finding, root string) (string, error) {
	if match := duplicatePattern.FindStringSubmatch(finding); match != nil {
		return duplicateIdentity(sources, finding, root, match)
	}

	file, ok := findings.FindingPath(finding)
	if !ok {
		return "", &findingLocationError{finding: finding}
	}

	location := strings.SplitN(strings.TrimPrefix(finding, file+":"), ":", findingLocationParts)
	line, err := strconv.Atoi(location[0])
	if err != nil {
		slog.Error(
			"notice parse source line",
			slog.String("finding", finding),
			slog.Any("error", err),
		)

		return "", fmt.Errorf("notice source line: %w", err)
	}

	column, err := strconv.Atoi(location[1])
	if err != nil {
		slog.Error(
			"notice parse source column",
			slog.String("finding", finding),
			slog.Any("error", err),
		)

		return "", fmt.Errorf("notice source column: %w", err)
	}

	parsed, err := loadSource(sources, root, file)
	if err != nil {
		return "", err
	}

	nodes := []ast.Node{parsed.file}
	if !strings.Contains(finding, ": file-length-limit:") {
		nodes, err = parsed.nodesAt(line, column)
		if err != nil {
			return "", err
		}
	}

	digest, err := parsed.fingerprint(nodes)
	if err != nil {
		return "", err
	}

	return SourceKey(finding) + "\x00" + digest, nil
}

func loadSource(sources map[string]Source, root, file string) (Source, error) {
	if cached, ok := sources[file]; ok {
		return cached, nil
	}

	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		slog.Error("notice read source", slog.String("file", file), slog.Any("error", err))

		return Source{}, fmt.Errorf("read notice source %s: %w", file, err)
	}

	files := token.NewFileSet()
	parsed, err := parser.ParseFile(
		files,
		file,
		contents,
		parser.ParseComments|parser.SkipObjectResolution,
	)
	if err != nil {
		slog.Error("notice parse source", slog.String("file", file), slog.Any("error", err))

		return Source{}, fmt.Errorf("parse notice source %s: %w", file, err)
	}

	ast.Inspect(parsed, expandParameters)
	result := Source{file: parsed, files: files}
	sources[file] = result

	return result, nil
}

func (parsed Source) nodesAt(line, column int) ([]ast.Node, error) {
	file := parsed.files.File(parsed.file.Pos())
	if line < 1 || line > file.LineCount() || column < 1 {
		return nil, &sourceLocationError{line: line, column: column}
	}

	position := file.LineStart(line) + token.Pos(column-1)
	if position >= parsed.file.Name.Pos() && position < parsed.file.Name.End() {
		return []ast.Node{parsed.file.Name}, nil
	}

	node, context := occurrence(parsed.file, position)
	if context != nil && node != context {
		header := *context
		header.Body = nil

		return []ast.Node{&header, node}, nil
	}

	return []ast.Node{node}, nil
}

func occurrence(file *ast.File, position token.Pos) (ast.Node, *ast.FuncDecl) {
	var node ast.Node = file
	var context *ast.FuncDecl
	ast.Inspect(
		file,
		func(candidate ast.Node) bool {
			if candidate == nil || position < commentStart(candidate) ||
				position >= candidate.End() {
				return false
			}

			switch typed := candidate.(type) {
			case ast.Decl:
				node = candidate
				if function, ok := typed.(*ast.FuncDecl); ok {
					context = function
				}
			case *ast.CallExpr:
				for _, argument := range typed.Args {
					literal, ok := argument.(*ast.FuncLit)
					if ok && position >= literal.Pos() && position < literal.End() {
						node = typed
					}
				}
			}

			return true
		},
	)

	return node, context
}
