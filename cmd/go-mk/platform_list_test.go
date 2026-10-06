package main_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGoMkPlatformsWritesTheModuleList(t *testing.T) {
	testCases := []struct {
		name      string
		makeArgs  []string
		wantLists string
	}{
		{name: "declared list", makeArgs: []string{"GO_MK_PLATFORMS=linux/amd64 freebsd/amd64"}, wantLists: "linux/amd64 freebsd/amd64"},
		{name: "release list wider than the gate list", makeArgs: []string{"GO_MK_PLATFORMS=linux/amd64", "RELEASE_PLATFORMS=linux/amd64 linux/arm64"}, wantLists: "linux/amd64 linux/arm64"},
		{name: "default list", makeArgs: nil, wantLists: "darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			consumerDir := newConsumerRepository(t, afterNoticeDate)
			outputPath := filepath.Join(t.TempDir(), "github-output")
			args := append([]string{"go-mk-platforms", "GITHUB_OUTPUT=" + outputPath}, testCase.makeArgs...)

			output, err := runConsumerMake(consumerDir, args...)
			if err != nil {
				t.Fatalf("make go-mk-platforms: %v\n%s", err, output)
			}
			if !strings.Contains(output, testCase.wantLists+"\n") {
				t.Fatalf("output lacks %q:\n%s", testCase.wantLists, output)
			}
			written := readConsumerFile(t, filepath.Dir(outputPath), filepath.Base(outputPath))
			if written != "platforms="+testCase.wantLists+"\n" {
				t.Fatalf("GITHUB_OUTPUT = %q, want platforms=%s", written, testCase.wantLists)
			}
		})
	}
}
