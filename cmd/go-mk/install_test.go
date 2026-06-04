package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseInstallBins(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		binary     string
		mainPkg    string
		installDir string
		want       []binSpec
		wantErr    bool
	}{
		{
			name:       "empty falls back to single binary",
			text:       "",
			binary:     "tool",
			mainPkg:    "./cmd/tool",
			installDir: "/bin",
			want:       []binSpec{{name: "tool", mainPkg: "./cmd/tool", dir: "/bin"}},
		},
		{
			name:       "empty with no binary is an error",
			text:       "",
			binary:     "",
			mainPkg:    ".",
			installDir: "/bin",
			wantErr:    true,
		},
		{
			name:       "two pairs install into the shared dir",
			text:       "daemon:./cmd/daemon cli:./cmd/cli",
			binary:     "daemon",
			mainPkg:    "./cmd/daemon",
			installDir: "/bin",
			want: []binSpec{
				{name: "daemon", mainPkg: "./cmd/daemon", dir: "/bin"},
				{name: "cli", mainPkg: "./cmd/cli", dir: "/bin"},
			},
		},
		{
			name:       "third field overrides the dir",
			text:       "tool:./cmd/tool:/opt/scripts",
			binary:     "tool",
			mainPkg:    "./cmd/tool",
			installDir: "/bin",
			want:       []binSpec{{name: "tool", mainPkg: "./cmd/tool", dir: "/opt/scripts"}},
		},
		{
			name:       "missing cmd is an error",
			text:       "tool",
			binary:     "tool",
			mainPkg:    "./cmd/tool",
			installDir: "/bin",
			wantErr:    true,
		},
		{
			name:       "empty cmd field is an error",
			text:       "tool:",
			binary:     "tool",
			mainPkg:    "./cmd/tool",
			installDir: "/bin",
			wantErr:    true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := parseInstallBins(testCase.text, testCase.binary, testCase.mainPkg, testCase.installDir)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("parseInstallBins(%q) expected error, got %v", testCase.text, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseInstallBins(%q) unexpected error: %v", testCase.text, err)
			}
			if len(got) != len(testCase.want) {
				t.Fatalf("parseInstallBins(%q) = %v, want %v", testCase.text, got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("parseInstallBins(%q)[%d] = %v, want %v", testCase.text, index, got[index], testCase.want[index])
				}
			}
		})
	}
}

func TestDirWritable(t *testing.T) {
	writable := t.TempDir()
	if !dirWritable(writable) {
		t.Fatalf("dirWritable(%q) = false, want true for a fresh temp dir", writable)
	}

	notYetCreated := filepath.Join(writable, "child", "grandchild")
	if !dirWritable(notYetCreated) {
		t.Fatalf("dirWritable(%q) = false, want true when the nearest ancestor is writable", notYetCreated)
	}

	if os.Geteuid() == 0 {
		t.Skip("running as root, so a read-only directory is still writable")
	}
	readOnly := filepath.Join(writable, "readonly")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatalf("mkdir read-only dir: %v", err)
	}
	target := filepath.Join(readOnly, "binary")
	if dirWritable(target) {
		t.Fatalf("dirWritable(%q) = true, want false under a read-only directory", target)
	}
}
