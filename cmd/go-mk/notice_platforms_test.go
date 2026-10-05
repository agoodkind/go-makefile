package main_test

import (
	"runtime"
	"strings"
	"testing"
)

const (
	cgoOnlyPackage = "package cgoonly\n\n// int one(void) { return 1; }\nimport \"C\"\n\nfunc One() int { return int(C.one()) }\n"
	cgoUserPackage = "package user\n\nimport \"example.com/consumer/cgoonly\"\n\nfunc Value() int { return cgoonly.One() }\n"
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

// For another platform, go-mk analyzes with cgo off, and the cgo package does
// not build.
func TestNoticeRecordsNoBuildErrorsFromAnotherPlatform(t *testing.T) {
	consumerDir := newConsumerRepository(t, beforeNoticeDate)
	writeConsumerFile(t, consumerDir, "cgoonly/cgoonly.go", cgoOnlyPackage)
	writeConsumerFile(t, consumerDir, "user/user.go", cgoUserPackage)

	output, err := runConsumerMake(consumerDir, "staticcheck-extra")
	if err != nil {
		t.Fatalf("gate failed after the notice: %v\n%s", err, output)
	}
	baseline := readConsumerFile(t, consumerDir, baselineFileName)
	if !strings.Contains(baseline, olderTestFinding) {
		t.Fatalf("notice output lacks %s:\n%s", olderTestFinding, baseline)
	}
	for _, unwanted := range []string{"user/user.go", "build constraints", "analysis skipped"} {
		if strings.Contains(baseline, unwanted) {
			t.Fatalf("notice output contains %q:\n%s", unwanted, baseline)
		}
	}
	if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != "1\n2\n" {
		t.Fatalf("applied notices = %q, want notices 1 and 2", applied)
	}
}

func TestNoticeStopsWhenAnotherPlatformPackageDoesNotBuild(t *testing.T) {
	otherOS := "darwin"
	if runtime.GOOS == "darwin" {
		otherOS = "linux"
	}
	consumerDir := newConsumerRepository(t, beforeNoticeDate)
	writeConsumerFile(t, consumerDir, "cgoonly/cgoonly.go", cgoOnlyPackage)
	writeConsumerFile(t, consumerDir, "user/user.go", cgoUserPackage)
	writeConsumerFile(t, consumerDir, "user/user_other_test.go",
		"//go:build "+otherOS+"\n\npackage user_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/consumer/user\"\n)\n\nfunc TestValue(t *testing.T) {\n\tif user.Value() != 1 {\n\t\tt.Fatal(\"Value() != 1\")\n\t}\n}\n")

	output, _ := runConsumerMake(consumerDir, "staticcheck-extra")
	if !strings.Contains(output, "staticcheck-extra cannot build ./user for "+otherOS+"/") {
		t.Fatalf("output lacks the build prerequisite for ./user:\n%s", output)
	}
	if applied := readConsumerFile(t, consumerDir, appliedNoticeFile); applied != "1\n" {
		t.Fatalf("applied notices = %q, want only notice 1", applied)
	}
}
