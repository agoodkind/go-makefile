package main_test

import (
	"strings"
	"testing"
)

func TestStaticcheckExtraReportsACompilerErrorOnce(t *testing.T) {
	consumerDir := newConsumerRepository(t, afterNoticeDate)
	writeConsumerFile(t, consumerDir, "widget/broken.go", "package widget\n\nfunc Broken() int { return missing }\n")
	writeConsumerFile(t, consumerDir, "caller/caller.go",
		"package caller\n\nimport \"example.com/consumer/widget\"\n\nfunc Value() int { return widget.Broken() }\n")

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err == nil {
		t.Fatalf("gate passed with a compiler error:\n%s", output)
	}
	for _, want := range []string{"staticcheck-extra could not build these packages", "widget/broken.go:3:28: undefined: missing"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"analysis skipped", "failed prerequisites", "New findings"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("output contains %q:\n%s", unwanted, output)
		}
	}
}
