package main_test

import (
	"path/filepath"
	"testing"
)

func TestReleaseAssetsListsEveryBinaryAndPlatform(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	consumerDir := newConsumerRepository(t, afterNoticeDate)
	makefile := "GO_MK_DEV_DIR := " + repoRoot + "\n_GO_MK_PROVISIONED := 1\n" +
		"BINARY := widget\nCMD := ./widget\n" +
		"RELEASE_BINS := widget:./widget widget-cgo:./widget:cgo=1,platforms=linux/amd64\n" +
		"include " + filepath.Join(repoRoot, "go.mk") + "\n" +
		"include " + filepath.Join(repoRoot, "go-release.mk") + "\n"
	writeConsumerFile(t, consumerDir, "Makefile", makefile)

	output, err := runConsumerMake(consumerDir, "--silent", "go-mk-release-assets", "RELEASE_PLATFORMS=linux/amd64 linux/arm64")
	if err != nil {
		t.Fatalf("make go-mk-release-assets: %v\n%s", err, output)
	}
	want := "widget_linux_amd64.tar.gz\nwidget_linux_arm64.tar.gz\nwidget-cgo_linux_amd64.tar.gz\n"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}
