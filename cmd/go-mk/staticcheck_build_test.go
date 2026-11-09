package main_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticcheckExtraReportsACompilerErrorOnce(t *testing.T) {
	consumerDir := newConsumerRepository(t, afterNoticeDate)
	writeConsumerFile(
		t,
		consumerDir,
		"widget/broken.go",
		"package widget\n\nfunc Broken() int { return missing }\n",
	)
	writeConsumerFile(
		t,
		consumerDir,
		"caller/caller.go",
		"package caller\n\nimport \"example.com/consumer/widget\"\n\nfunc Value() int { return widget.Broken() }\n",
	)

	writeConsumerFile(
		t,
		consumerDir,
		baselineFileName,
		"unseen/source.go:1:1: doc_structure: add an introduction, explanation, and example or layout\n",
	)
	arguments := append(
		staticcheckResultArguments(t),
		"STATICCHECK_EXTRA_EXCLUDE_PATHS=widget/broken.go:",
	)
	output, err := runConsumerMake(consumerDir, arguments...)
	if err == nil {
		t.Fatalf("gate passed with a compiler error:\n%s", output)
	}

	for _, want := range []string{
		"analysis did not complete (exit status 1)",
		"Package loading or compiler errors:",
		"widget/broken.go:3:28: undefined: missing",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}

	for _, unwanted := range []string{
		"analysis skipped",
		"failed prerequisites",
		"now fixed",
		"Analyzer or tool errors:",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("output contains %q:\n%s", unwanted, output)
		}
	}

	if count := strings.Count(output, "undefined: missing"); count != 1 {
		t.Fatalf("compiler error appeared %d times:\n%s", count, output)
	}

	if observed := readConsumerFile(
		t,
		consumerDir,
		".make/staticcheck-extra.out",
	); strings.TrimSpace(observed) != "" {
		t.Fatalf("compiler errors became lint diagnostics:\n%s", observed)
	}
}

func TestStaticcheckExtraReportsACompletedAnalysis(t *testing.T) {
	consumerDir := newConsumerRepository(t, afterNoticeDate)
	writeConsumerFile(
		t,
		consumerDir,
		"widget/widget.go",
		`package widget

// Double returns twice the input.
//
// It multiplies the supplied value by two.
//
// For example:
//
//     Double(3)
func Double(value int) int { return value * 2 }
`,
	)
	writeConsumerFile(
		t,
		consumerDir,
		baselineFileName,
		"unseen/source.go:1:1: doc_structure: add an introduction, explanation, and example or layout\n",
	)
	output, err := runConsumerMake(consumerDir, staticcheckResultArguments(t)...)
	if err != nil {
		t.Fatalf("complete successful analysis failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "staticcheck-extra: OK") ||
		!strings.Contains(output, "Saved findings now fixed: 1") ||
		strings.Contains(output, "did not complete") {
		t.Fatalf("successful analysis lost its complete verdict:\n%s", output)
	}
}

func staticcheckResultArguments(t *testing.T) []string {
	t.Helper()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	return []string{
		"staticcheck-extra",
		"STATICCHECK_EXTRA_BUILD_REPO=" + filepath.Join(repository, "staticcheck"),
		"STATICCHECK_EXTRA_BUILD_PKG=./cmd/staticcheck-extra",
		"STATICCHECK_EXTRA_FLAGS=-doc_structure",
		"STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN=doc_structure:",
	}
}
