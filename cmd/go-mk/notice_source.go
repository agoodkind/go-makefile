package main

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/findings"
)

type noticeSource struct {
	contents []byte
	file     *ast.File
	fileSet  *token.FileSet
}

type noticeSources map[string]noticeSource

// Baselines group equal diagnostics within a file. Match each occurrence to
// unchanged source before accepting that group without a baseline token.
func (sources noticeSources) identity(finding, root string) (string, error) {
	key := baseline.Key(finding)
	file, ok := findings.FindingPath(finding)
	if !ok {
		return "", fmt.Errorf("notice cannot locate finding: %s", finding)
	}
	location := strings.SplitN(strings.TrimPrefix(finding, file+":"), ":", 3)
	line, err := strconv.Atoi(location[0])
	if err != nil {
		return "", err
	}
	column, err := strconv.Atoi(location[1])
	if err != nil {
		return "", err
	}
	source, loaded := sources[file]
	if !loaded {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return "", err
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, file, contents, parser.SkipObjectResolution)
		if err != nil {
			return "", err
		}
		source = noticeSource{contents: contents, file: parsed, fileSet: fileSet}
		sources[file] = source
	}
	tokenFile := source.fileSet.File(source.file.Pos())
	if line < 1 || line > tokenFile.LineCount() || column < 1 {
		return "", fmt.Errorf("notice finding has an invalid source location: %s", finding)
	}
	position := tokenFile.LineStart(line) + token.Pos(column-1)
	var node ast.Node = source.file
	context := ""
	if position >= source.file.Name.Pos() && position < source.file.Name.End() {
		node = source.file.Name
	} else {
		ast.Inspect(source.file, func(candidate ast.Node) bool {
			if candidate == nil {
				return false
			}
			if position < candidate.Pos() || position >= candidate.End() {
				return false
			}
			switch typed := candidate.(type) {
			case ast.Decl:
				node = candidate
				if function, ok := typed.(*ast.FuncDecl); ok {
					start := source.fileSet.Position(function.Pos()).Offset
					end := source.fileSet.Position(function.Type.End()).Offset
					context = string(source.contents[start:end])
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
		})
	}
	start := source.fileSet.Position(node.Pos()).Offset
	end := source.fileSet.Position(node.End()).Offset
	digest := sha256.Sum256([]byte(context + "\x00" + string(source.contents[start:end])))
	return fmt.Sprintf("%s\x00%x", key, digest), nil
}
