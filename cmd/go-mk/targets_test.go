package main_test

import (
	"runtime"
	"strings"
	"testing"
)

const fixtureCommandMain = `package main
import "example.com/consumer/widget"
func main() { widget.Double(1) }
`

// TestNoticeRecordsProductionFindingsForAnotherPlatform checks historical
// platform files.
func TestNoticeRecordsProductionFindingsForAnotherPlatform(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	previous := setPendingNotices(t, consumer, commandNotice)
	otherOS := fixtureLinux
	if runtime.GOOS == otherOS {
		otherOS = fixtureDarwin
	}

	historicalFile := "cmd/tool/platform_" + otherOS + ".go"
	writeConsumerFile(t, consumer, fixtureCommand, fixtureCommandMain)
	header := "//go:build " + otherOS + "\n\npackage main\n\n"
	writeConsumerFile(t, consumer, historicalFile, header+"func historical() int { return 1 }\n")
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical platform source")
	flags := "STATICCHECK_EXTRA_FLAGS=-commandentry"
	testFlags := fixtureTestFlagsOff
	target := otherOS + "/" + runtime.GOARCH
	host := runtime.GOOS + "/" + runtime.GOARCH
	output, err := runConsumerMake(consumer, "go-mk-notice", flags, testFlags, "GO_MK_PLATFORMS="+host+" "+target)
	if err != nil {
		t.Fatalf("host notice failed: %v\n%s", err, output)
	}

	if applied := readConsumerFile(t, consumer, appliedNoticeFile); applied != previous+commandNotice+"\n" {
		t.Fatalf("host notice did not apply commandentry:\n%s\n%s", applied, output)
	}

	baseline := readConsumerFile(t, consumer, baselineFileName)
	if !strings.Contains(baseline, historicalFile+":") || !strings.Contains(baseline, "commandentry:") {
		t.Fatalf("host notice omitted the historical production finding:\n%s\n%s", baseline, output)
	}

	arguments := []string{fixtureStaticcheckGate, flags, testFlags, "GO_MK_PLATFORMS=" + target}
	if output, err := runConsumerMake(consumer, arguments...); err != nil {
		t.Fatalf("historical production finding failed its target gate: %v\n%s", err, output)
	}

	laterFile := "cmd/tool/later_" + otherOS + ".go"
	writeConsumerFile(t, consumer, laterFile, header+"func later() int { return 2 }\n")
	output, err = runConsumerMake(consumer, arguments...)
	if err == nil || !strings.Contains(output, laterFile+":") || !strings.Contains(output, "commandentry:") {
		t.Fatalf("target gate accepted the later production finding: %v\n%s", err, output)
	}

	if current := readConsumerFile(t, consumer, baselineFileName); current != baseline {
		t.Fatalf("target gate changed the historical baseline:\n%s", current)
	}
}

// TestNoticeRejectsOtherPlatformPackageLoadErrors checks a missing
// platform dependency.
func TestNoticeRejectsOtherPlatformPackageLoadErrors(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	previous := setPendingNotices(t, consumer, commandNotice)
	otherOS := fixtureLinux
	if runtime.GOOS == otherOS {
		otherOS = fixtureDarwin
	}

	writeConsumerFile(t, consumer, fixtureCommand, fixtureCommandMain)
	const dependency = `package main
import "example.com/consumer/missing"
func historical() { missing.Run() }
`
	writeConsumerFile(t, consumer, "cmd/tool/platform_"+otherOS+".go", "//go:build "+otherOS+"\n\n"+dependency)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add unavailable platform dependency")
	target := otherOS + "/" + runtime.GOARCH
	host := runtime.GOOS + "/" + runtime.GOARCH
	output, err := runConsumerMake(
		consumer,
		"go-mk-notice",
		"STATICCHECK_EXTRA_FLAGS=-commandentry",
		fixtureTestFlagsOff,
		"GO_MK_PLATFORMS="+host+" "+target,
	)
	if err != nil {
		t.Fatalf("host notice command failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "load notice source package") ||
		!strings.Contains(output, "example.com/consumer/missing") {
		t.Fatalf("notice did not report the package loading error:\n%s", output)
	}

	if applied := readConsumerFile(t, consumer, appliedNoticeFile); applied != previous {
		t.Fatalf("notice accepted an incomplete source inventory:\n%s", applied)
	}
}
