package staticcheck

import (
	"strings"

	"golang.org/x/tools/go/analysis"
)

// NolintBanAnalyzer flags any //nolint comment in production code.
// Suppressing linter findings inline hides real problems and makes
// the lint signal untrustworthy; fix the underlying finding instead.
//
// Skipped for: _test.go, code-generated files, protobuf-generated
// files, and the staticcheck-extra analyzer source itself.
var NolintBanAnalyzer = &analysis.Analyzer{
	Name: "nolint_ban",
	Doc:  "rejects //nolint comments in production code",
	Run:  runNolintBan,
}

func runNolintBan(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if !shouldAnalyzeFile(pass, file) {
			continue
		}
		for _, group := range file.Comments {
			for _, c := range group.List {
				if strings.Contains(c.Text, "//nolint") {
					reportAtf(pass, file, c.Pos(), "nolint comment suppresses linter findings inline; remove it and fix the underlying finding")
				}
			}
		}
	}
	return nil, nil
}
