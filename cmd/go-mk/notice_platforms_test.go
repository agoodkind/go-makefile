package main_test

import (
	"strings"
	"testing"
)

// The notice runs in the freebsd pass on every host. That pass does not build
// the linux test file.
func TestNoticeRecordsTestFindingsInFilesForAnotherPlatform(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)
	writeConsumerFile(t, consumerDir, "widget/widget_linux_test.go",
		"//go:build linux\n\npackage widget\n\nimport \"testing\"\n\nfunc TestDoubleOnLinux(t *testing.T) {\n\tif Double(5) != 10 {\n\t\tt.Fatal(\"Double(5) != 10\")\n\t}\n}\n")

	output, err := runConsumerMake(consumerDir, "staticcheck-extra", "GO_MK_PLATFORMS=freebsd/amd64 linux/amd64")
	if err != nil {
		t.Fatalf("gate failed after the notice: %v\n%s", err, output)
	}
	baseline := readConsumerFile(t, consumerDir, baselineFileName)
	for _, want := range []string{olderTestFinding, "widget/widget_linux_test.go:3:9"} {
		if !strings.Contains(baseline, want) {
			t.Fatalf("notice output lacks %s:\n%s", want, baseline)
		}
	}
}
