package main_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoticeRecordsProductionFindingsForAnotherPlatform(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	notices, err := os.ReadFile(filepath.Join(repository, "notices.txt"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, "notices.txt", string(notices))
	writeConsumerFile(t, consumer, appliedNoticeFile, "1\n2\n4\n5\n6\n")
	otherOS := "linux"
	if runtime.GOOS == otherOS {
		otherOS = "darwin"
	}

	historicalFile := "cmd/tool/platform_" + otherOS + ".go"
	writeConsumerFile(t, consumer, "cmd/tool/main.go", "package main\n\nimport \"example.com/consumer/widget\"\n\nfunc main() { widget.Double(1) }\n")
	writeConsumerFile(t, consumer, historicalFile, "//go:build "+otherOS+"\n\npackage main\n\nfunc historical() int { return 1 }\n")
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical platform source")
	flags := "STATICCHECK_EXTRA_FLAGS=-commandentry"
	testFlags := "STATICCHECK_EXTRA_TEST_FLAGS="
	target := otherOS + "/" + runtime.GOARCH
	host := runtime.GOOS + "/" + runtime.GOARCH
	output, err := runConsumerMake(consumer, "go-mk-notice", flags, testFlags, "GO_MK_PLATFORMS="+host+" "+target)
	if err != nil {
		t.Fatalf("host notice failed: %v\n%s", err, output)
	}

	if applied := readConsumerFile(t, consumer, appliedNoticeFile); !strings.Contains(applied, "\n3\n") {
		t.Fatalf("host notice did not apply commandentry:\n%s\n%s", applied, output)
	}

	baseline := readConsumerFile(t, consumer, baselineFileName)
	if !strings.Contains(baseline, historicalFile+":") || !strings.Contains(baseline, "commandentry:") {
		t.Fatalf("host notice omitted the historical production finding:\n%s\n%s", baseline, output)
	}

	arguments := []string{"staticcheck-extra", flags, testFlags, "GO_MK_PLATFORMS=" + target}
	if output, err := runConsumerMake(consumer, arguments...); err != nil {
		t.Fatalf("historical production finding failed its target gate: %v\n%s", err, output)
	}

	laterFile := "cmd/tool/later_" + otherOS + ".go"
	writeConsumerFile(t, consumer, laterFile, "//go:build "+otherOS+"\n\npackage main\n\nfunc later() int { return 2 }\n")
	output, err = runConsumerMake(consumer, arguments...)
	if err == nil || !strings.Contains(output, laterFile+":") || !strings.Contains(output, "commandentry:") {
		t.Fatalf("target gate accepted the later production finding: %v\n%s", err, output)
	}

	if current := readConsumerFile(t, consumer, baselineFileName); current != baseline {
		t.Fatalf("target gate changed the historical baseline:\n%s", current)
	}
}

func TestNoticeRejectsOtherPlatformPackageLoadErrors(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	notices, err := os.ReadFile(filepath.Join(repository, "notices.txt"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, "notices.txt", string(notices))
	previous := "1\n2\n4\n5\n6\n"
	writeConsumerFile(t, consumer, appliedNoticeFile, previous)
	otherOS := "linux"
	if runtime.GOOS == otherOS {
		otherOS = "darwin"
	}

	writeConsumerFile(t, consumer, "cmd/tool/main.go", "package main\n\nimport \"example.com/consumer/widget\"\n\nfunc main() { widget.Double(1) }\n")
	writeConsumerFile(t, consumer, "cmd/tool/platform_"+otherOS+".go", "//go:build "+otherOS+"\n\npackage main\n\nimport \"example.com/consumer/missing\"\n\nfunc historical() { missing.Run() }\n")
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add unavailable platform dependency")
	target := otherOS + "/" + runtime.GOARCH
	host := runtime.GOOS + "/" + runtime.GOARCH
	output, err := runConsumerMake(consumer, "go-mk-notice", "STATICCHECK_EXTRA_FLAGS=-commandentry", "STATICCHECK_EXTRA_TEST_FLAGS=", "GO_MK_PLATFORMS="+host+" "+target)
	if err != nil {
		t.Fatalf("host notice command failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "load notice source package") || !strings.Contains(output, "example.com/consumer/missing") {
		t.Fatalf("notice did not report the package loading error:\n%s", output)
	}

	if applied := readConsumerFile(t, consumer, appliedNoticeFile); applied != previous {
		t.Fatalf("notice accepted an incomplete source inventory:\n%s", applied)
	}
}
