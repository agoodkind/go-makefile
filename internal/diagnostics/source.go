package diagnostics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

const whitespaceTag = " (wsl_v5)"

var sourceLocation = regexp.MustCompile(`^(.+\.go):([0-9]+):([0-9]+): `)

type source struct {
	fileSet      *token.FileSet
	file         *ast.File
	declarations map[ast.Decl]declaration
}

type declaration struct {
	fingerprint string
	statements  []statement
}

type statement struct {
	node        ast.Stmt
	fingerprint string
	ordinal     int
}

func whitespaceIdentity(line, root string, sources map[string]*source) (string, error) {
	if !strings.HasSuffix(line, whitespaceTag) {
		return line, nil
	}

	location := sourceLocation.FindStringSubmatch(line)
	if location == nil {
		return "", fmt.Errorf("cannot locate whitespace finding: %s", line)
	}

	path := location[1]
	parsed, loaded := sources[path]
	if !loaded {
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, filepath.Join(root, path), nil, parser.SkipObjectResolution)
		if err != nil {
			return "", failure("parse whitespace source "+path, err)
		}

		parsed = &source{fileSet: fileSet, file: file, declarations: make(map[ast.Decl]declaration)}
		sources[path] = parsed
	}

	lineNumber, err := strconv.Atoi(location[2])
	if err != nil {
		return "", failure("read whitespace source line", err)
	}

	column, err := strconv.Atoi(location[3])
	if err != nil {
		return "", failure("read whitespace source column", err)
	}

	identity, err := parsed.identity(lineNumber, column, strings.Contains(line, "unnecessary whitespace"))
	if err != nil {
		return "", failure("identify whitespace finding "+line, err)
	}

	return strings.TrimSuffix(line, whitespaceTag) + " [source:" + identity + "]" + whitespaceTag, nil
}

func (parsed *source) identity(line, column int, adjacent bool) (string, error) {
	file := parsed.fileSet.File(parsed.file.Pos())
	if line < 1 || line > file.LineCount() || column < 1 {
		return "", fmt.Errorf("invalid source location")
	}

	position := file.LineStart(line) + token.Pos(column-1)
	if position > file.Pos(file.Size()) {
		return "", fmt.Errorf("source column exceeds file size")
	}

	for index, node := range parsed.file.Decls {
		if position < node.Pos() || position > node.End() {
			continue
		}

		context, loaded := parsed.declarations[node]
		if !loaded {
			var err error
			context, err = analyzeDeclaration(node, parsed.file.Decls[:index])
			if err != nil {
				return "", err
			}

			parsed.declarations[node] = context
		}

		selected, found := selectStatement(context.statements, position, adjacent)
		if !found {
			return "", fmt.Errorf("cannot bind whitespace finding to a statement")
		}

		digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", context.fingerprint, selected.fingerprint, selected.ordinal)))

		return hex.EncodeToString(digest[:]), nil
	}

	return "", fmt.Errorf("cannot bind whitespace finding to a declaration")
}

func analyzeDeclaration(node ast.Decl, preceding []ast.Decl) (declaration, error) {
	fingerprint, err := declarationIdentity(node, preceding)
	if err != nil {
		return declaration{}, err
	}

	result := declaration{fingerprint: fingerprint, statements: nil}
	// The count includes statements that already use correct spacing.
	occurrences := make(map[string]int)
	var visitErr error
	ast.Inspect(node, func(
		candidate ast.Node,
	) bool {
		if visitErr != nil {
			return false
		}

		current, ok := candidate.(ast.Stmt)
		if !ok {
			return true
		}

		fingerprint, err := canonical(current)
		if err != nil {
			visitErr = err
			return false
		}

		occurrences[fingerprint]++
		result.statements = append(result.statements, statement{
			node: current, fingerprint: fingerprint, ordinal: occurrences[fingerprint],
		})

		return true
	})
	if visitErr != nil {
		return declaration{}, visitErr
	}

	return result, nil
}

func declarationIdentity(node ast.Decl, preceding []ast.Decl) (string, error) {
	fingerprint, err := canonical(node)
	if err != nil {
		return "", err
	}

	function, isFunction := node.(*ast.FuncDecl)
	ordinal := 1

	for _, earlier := range preceding {
		prior, wasFunction := earlier.(*ast.FuncDecl)
		if wasFunction != isFunction || (isFunction && prior.Name.Name != function.Name.Name) {
			continue
		}

		identity, err := canonical(earlier)
		if err != nil {
			return "", err
		}

		if identity == fingerprint {
			ordinal++
		}
	}

	if ordinal == 1 {
		return fingerprint, nil
	}

	return fmt.Sprintf("%s\x00%d", fingerprint, ordinal), nil
}

func canonical(node ast.Node) (string, error) {
	var output bytes.Buffer
	err := ast.Fprint(&output, nil, node, func(
		_ string,
		value reflect.Value,
	) bool {
		return value.Type() != reflect.TypeFor[token.Pos]()
	})
	if err != nil {
		return "", failure("encode whitespace AST", err)
	}

	return output.String(), nil
}

func selectStatement(statements []statement, position token.Pos, adjacent bool) (statement, bool) {
	var selected statement
	found := false

	for _, candidate := range statements {
		if adjacent {
			if candidate.node.Pos() >= position && (!found || candidate.node.Pos() < selected.node.Pos()) {
				selected, found = candidate, true
			}

			continue
		}

		if candidate.node.Pos() <= position && position < candidate.node.End() {
			if !found || candidate.node.End()-candidate.node.Pos() < selected.node.End()-selected.node.Pos() {
				selected, found = candidate, true
			}
		}
	}

	return selected, found
}
