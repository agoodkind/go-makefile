package notice

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
)

type commentIdentity struct {
	text   string
	before string
	after  string
}

func (parsed Source) comments(
	nodes []ast.Node,
	positions []syntaxPosition,
) ([]commentIdentity, error) {
	var result []commentIdentity

	for _, group := range parsed.file.Comments {
		included := group.Pos() < parsed.file.Package

		for _, node := range nodes {
			start := commentStart(node)
			if group.End() >= start &&
				parsed.files.Position(group.Pos()).Line <= parsed.files.Position(node.End()).Line {
				included = true
			}
		}

		if !included {
			continue
		}

		comments, err := normalizedComments(group)
		if err != nil {
			return nil, err
		}

		anchor := commentAnchors(group, positions)

		for _, text := range comments {
			result = append(
				result,
				commentIdentity{text: text, before: anchor.before, after: anchor.after},
			)
		}
	}

	return result, nil
}

func commentStart(node ast.Node) token.Pos {
	switch declaration := node.(type) {
	case *ast.FuncDecl:
		if declaration.Doc != nil {
			return declaration.Doc.Pos()
		}
	case *ast.GenDecl:
		if declaration.Doc != nil {
			return declaration.Doc.Pos()
		}
	}

	return node.Pos()
}

func normalizedComments(group *ast.CommentGroup) ([]string, error) {
	var contents strings.Builder
	contents.WriteString("package identity\n")

	for _, comment := range group.List {
		contents.WriteString(comment.Text + "\n")
	}

	contents.WriteString("func identity() {}\n")
	formatted, err := format.Source([]byte(contents.String()))
	if err != nil {
		slog.Error("notice format source comments", slog.Any("error", err))

		return nil, fmt.Errorf("format historical Go comments: %w", err)
	}

	parsed, err := parser.ParseFile(
		token.NewFileSet(),
		"identity.go",
		formatted,
		parser.ParseComments|parser.SkipObjectResolution,
	)
	if err != nil {
		slog.Error("notice parse source comments", slog.Any("error", err))

		return nil, fmt.Errorf("parse historical Go comments: %w", err)
	}

	var result []string

	for _, comments := range parsed.Comments {
		for _, comment := range comments.List {
			result = append(result, comment.Text)
		}
	}

	return result, nil
}
