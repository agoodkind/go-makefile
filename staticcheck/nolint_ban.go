package staticcheck

import (
	"strings"

	"golang.org/x/tools/go/analysis"
)

// NolintBanAnalyzer flags any //nolint comment in production code.
// Suppressing linter findings inline hides real problems and makes
// the lint baseline untrustworthy; use the staticcheck-extra baseline
// or a golangci-lint issue-exclusion rule to document intentional
// exceptions instead.
//
// Skipped for: _test.go, code-generated files, protobuf-generated
// files, and the staticcheck-extra analyzer source itself.
var NolintBanAnalyzer = &analysis.Analyzer{
	Name: "nolint_ban",
	Doc:  "rejects //nolint comments in production code; document exceptions in the staticcheck-extra baseline instead",
	Run:  runNolintBan,
}

func runNolintBan(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		path := fileName(pass, file.Pos())
		if isTestFile(path) || isGeneratedFile(file, path) || isProtobufGeneratedPath(path) || isStaticcheckPath(path) {
			continue
		}
		for _, group := range file.Comments {
			for _, c := range group.List {
				if strings.Contains(c.Text, "//nolint") {
					reportAtf(pass, file, c.Pos(), "nolint comment suppresses linter findings inline; document exceptions in the staticcheck-extra baseline or a golangci-lint issue-exclusion rule instead")
				}
			}
		}
	}
	return nil, nil
}
