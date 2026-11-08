package main_test

import (
	"path/filepath"
	"regexp"
	"testing"
)

// TestConsumerCommandInjectionFindings checks both locally exempted call
// sites in a consumer that does not validate its command arguments.
func TestConsumerCommandInjectionFindings(t *testing.T) {
	t.Parallel()
	consumer := newConsumerRepository(t, afterNoticeDate)
	lintPolicyConfiguration(t, consumer)
	files := []string{"internal/analyzer/source.go", "internal/notice/prepare.go"}

	for _, file := range files {
		source := readConsumerFile(t, filepath.Join("testdata", "consumer", "security"), file)
		writeConsumerFile(t, consumer, file, source)
	}

	output, err := runConsumerMake(
		consumer,
		fixtureGolangciGate,
		"GOLANGCI_LINT_TARGETS=./internal/...",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=gosec",
	)
	if err == nil {
		t.Fatalf("the lint gate accepted unvalidated command arguments:\n%s", output)
	}

	for _, file := range files {
		finding := regexp.MustCompile(regexp.QuoteMeta(file) + `:\d+:\d+\s+G702:`)
		if !finding.MatchString(output) {
			t.Errorf("the lint gate did not report G702 in %s:\n%s", file, output)
		}
	}
}
