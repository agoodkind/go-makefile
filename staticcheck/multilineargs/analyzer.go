// Package multilineargs keeps short calls compact and expands wrapped Go lists.
package multilineargs

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"

	"golang.org/x/tools/go/analysis"
)

const (
	analyzerName      = "multiline_call_arguments"
	diagnosticMessage = "Keep short simple calls compact, and place wrapped list items on separate lines."
	fixMessage        = "Apply readable Go list layout"
)

var errLayoutConvergence = errors.New("source layout did not converge")

// Analyzer checks compact calls and wrapped lists in production code and tests.
var Analyzer = &analysis.Analyzer{
	Name: analyzerName,
	Doc:  "keeps short simple calls compact and places wrapped list items on separate lines",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}

		if err := analyzeFile(pass, file); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func analyzeFile(pass *analysis.Pass, file *ast.File) error {
	tokenFile := pass.Fset.File(file.Pos())
	filename := tokenFile.Name()

	// Analysis drivers may omit the optional ReadFile callback.
	readFile := os.ReadFile
	if pass.ReadFile != nil {
		readFile = pass.ReadFile
	}

	source, err := readFile(filename)
	if err != nil {
		return fmt.Errorf("read call arguments in %s: %w", filename, err)
	}

	formatted, err := formatCallArguments(file, tokenFile, source)
	if err != nil {
		return fmt.Errorf("format call arguments in %s: %w", filename, err)
	}

	if bytes.Equal(source, formatted) {
		return nil
	}

	pass.Report(analysis.Diagnostic{
		Pos:     file.Package,
		Message: diagnosticMessage,
		SuggestedFixes: []analysis.SuggestedFix{
			{
				Message: fixMessage,
				TextEdits: []analysis.TextEdit{
					{
						Pos:     tokenFile.Pos(0),
						End:     tokenFile.Pos(tokenFile.Size()),
						NewText: formatted,
					},
				},
			},
		},
	})

	return nil
}

func formatCallArguments(file *ast.File, tokenFile *token.File, source []byte) ([]byte, error) {
	for range maximumLayoutRounds {
		planner := layoutPlanner{
			file:    file,
			tokens:  tokenFile,
			source:  source,
			edits:   nil,
			forced:  make(map[*ast.CompositeLit]bool),
			columns: make(map[token.Pos]int),
			err:     nil,
		}
		ast.Inspect(file, planner.visit)
		if planner.err != nil {
			return nil, planner.err
		}

		if len(planner.edits) == 0 {
			return source, nil
		}

		rewritten, err := applyLayoutEdits(source, planner.edits)
		if err != nil {
			return nil, err
		}

		formatted, err := format.Source(rewritten)
		if err != nil {
			return nil, fmt.Errorf("format readable Go lists: %w", err)
		}

		if bytes.Equal(source, formatted) {
			return source, nil
		}

		source = formatted
		files := token.NewFileSet()
		file, err = parser.ParseFile(
			files,
			tokenFile.Name(),
			source,
			parser.ParseComments|parser.SkipObjectResolution,
		)
		if err != nil {
			return nil, fmt.Errorf("parse readable Go lists: %w", err)
		}

		tokenFile = files.File(file.Pos())
	}

	return nil, fmt.Errorf("%w after %d rounds", errLayoutConvergence, maximumLayoutRounds)
}
