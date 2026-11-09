package main_test

import (
	"go/format"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoricalNoticeRunsOnlyItsScopedAnalyzer(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	const source = `package widget
import "strings"
type RequestEvent struct { DerivedCacheCreationTokens int; Stream bool }
// Historical returns an event.
func Historical(streamRequested bool, contentType string) RequestEvent {
return RequestEvent{
DerivedCacheCreationTokens: 0,
Stream: streamRequested || strings.Contains(contentType, "text/event-stream"),
}
}
`
	initial, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, "widget/history.go", string(initial))
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "-m", "Add historical source")
	writeConsumerFile(
		t,
		consumer,
		"widget/later.go",
		"package widget\n// Later returns a value.\nfunc Later() int { return 1 }\n",
	)
	runConsumerGit(t, consumer, afterNoticeDate, "add", ".")
	runConsumerGit(t, consumer, afterNoticeDate, "commit", "-m", "Add later source")
	writeConsumerFile(
		t,
		consumer,
		"notices.txt",
		"201\tINTRODUCED=2026-11-07T13:41:31-08:00 GATE=staticcheck-extra PATTERN=doc_structure:\tRequire structured comments\n",
	)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	arguments := []string{
		"staticcheck-extra",
		"STATICCHECK_EXTRA_BUILD_REPO=" + filepath.Join(repository, "staticcheck"),
		"STATICCHECK_EXTRA_BUILD_PKG=./cmd/staticcheck-extra",
		"STATICCHECK_EXTRA_FLAGS=-doc_structure -multiline_call_arguments",
		"_GO_MK_NOTICES_FILE=" + filepath.Join(consumer, "notices.txt"),
	}
	output, err := runConsumerMake(consumer, arguments...)
	if err == nil || !strings.Contains(output, "Keep short simple calls compact") {
		t.Fatalf("the full current gate did not retain its layout failure: %v\n%s", err, output)
	}

	if strings.Contains(output, "auto-baseline failed") {
		t.Fatalf("an unrelated analyzer blocked the scoped historical capture:\n%s", output)
	}

	baseline := readConsumerFile(t, consumer, baselineFileName)
	if !strings.Contains(baseline, "widget/history.go:") ||
		strings.Contains(baseline, "widget/later.go:") {
		t.Fatalf("historical baseline eligibility changed:\n%s", baseline)
	}

	arguments = append(arguments, "STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN=doc_structure:")
	output, err = runConsumerMake(consumer, arguments...)
	if err == nil || !strings.Contains(output, "widget/later.go:") ||
		strings.Contains(output, "widget/history.go:") {
		t.Fatalf("the later finding did not remain a gate failure: %v\n%s", err, output)
	}

	if raw := readConsumerFile(t, consumer, ".make/staticcheck-extra.raw.out"); strings.Contains(
		raw,
		"Keep short simple calls compact",
	) {
		t.Fatalf("the scoped capture executed an unrelated analyzer:\n%s", raw)
	}
}
