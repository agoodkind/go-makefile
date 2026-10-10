package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestInstallAllRunsHooksAroundInstalls(t *testing.T) {
	cfg, logPath := installFixDist(t)
	if err := installAll(cfg); err != nil {
		t.Fatalf("installAll: %v", err)
	}
	installFixAssertLog(t, logPath, "pre\nfirst\nsecond\npost\n")
	for _, bin := range cfg.bins {
		source, err := os.ReadFile(filepath.Join(cfg.distDir, bin.name))
		if err != nil {
			t.Fatal(err)
		}
		installed, err := os.ReadFile(filepath.Join(bin.dir, bin.name))
		if err != nil {
			t.Fatal(err)
		}
		if string(installed) != string(source) {
			t.Fatalf("installed %q = %q, want dist contents %q", bin.name, installed, source)
		}
	}
}

func TestInstallAllRunsPostHookAfterInstallFailure(t *testing.T) {
	cfg, logPath := installFixDist(t)
	t.Setenv("INSTALL_FIX_RUN_BINARIES", "0")
	t.Setenv("INSTALL_FIX_POST_STATUS", "31")
	missingSource := filepath.Join(cfg.distDir, cfg.bins[0].name)
	if err := os.Remove(missingSource); err != nil {
		t.Fatal(err)
	}

	err := installAll(cfg)
	installFixAssertLog(t, logPath, "pre\npost\n")
	installFixAssertAbsent(t, cfg)
	var installErr *os.PathError
	if !errors.As(err, &installErr) || installErr.Path != missingSource || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installAll error = %v, want missing dist file %q", err, missingSource)
	}
	var postErr *exec.ExitError
	if !errors.As(err, &postErr) || postErr.ExitCode() != 31 {
		t.Fatalf("installAll error = %v, want post hook exit status 31", err)
	}
}

func TestInstallAllSkipsInstallAndPostWhenPreHookFails(t *testing.T) {
	cfg, logPath := installFixDist(t)
	t.Setenv("INSTALL_FIX_PRE_STATUS", "23")
	err := installAll(cfg)
	installFixAssertLog(t, logPath, "pre\n")
	installFixAssertAbsent(t, cfg)
	var preErr *exec.ExitError
	if !errors.As(err, &preErr) || preErr.ExitCode() != 23 {
		t.Fatalf("installAll error = %v, want pre hook exit status 23", err)
	}
}

func restoreInstallSeams(t *testing.T) {
	t.Helper()
	originalInstallOneFunc := installOneFunc
	originalRunInstallHookFunc := runInstallHookFunc
	t.Cleanup(func() {
		installOneFunc = originalInstallOneFunc
		runInstallHookFunc = originalRunInstallHookFunc
	})
}

func installFixDist(t *testing.T) (installConfig, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks require sh")
	}
	root := t.TempDir()
	distDir := filepath.Join(root, "dist")
	installDir := filepath.Join(root, "installed")
	logPath := filepath.Join(root, "hooks.log")
	if err := os.Mkdir(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		installFixWriteScript(t, filepath.Join(distDir, name), "printf '%s\\n' '"+name+"'\n")
	}
	prePath := filepath.Join(root, "pre.sh")
	installFixWriteScript(t, prePath, `for name in first second; do
    if test -e "$INSTALL_FIX_DIR/$name"; then
        printf 'pre hook found an installed binary: %s\n' "$name" >&2
        exit 1
    fi
done
printf 'pre\n' >> "$INSTALL_FIX_LOG"
exit "$INSTALL_FIX_PRE_STATUS"
`)
	postPath := filepath.Join(root, "post.sh")
	installFixWriteScript(t, postPath, `if test "$INSTALL_FIX_RUN_BINARIES" = 1; then
    "$INSTALL_FIX_DIR/first" >> "$INSTALL_FIX_LOG"
    "$INSTALL_FIX_DIR/second" >> "$INSTALL_FIX_LOG"
fi
printf 'post\n' >> "$INSTALL_FIX_LOG"
exit "$INSTALL_FIX_POST_STATUS"
`)
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("INSTALL_FIX_DIR", installDir)
	t.Setenv("INSTALL_FIX_LOG", logPath)
	t.Setenv("INSTALL_FIX_PRE_HOOK", prePath)
	t.Setenv("INSTALL_FIX_POST_HOOK", postPath)
	t.Setenv("INSTALL_FIX_PRE_STATUS", "0")
	t.Setenv("INSTALL_FIX_POST_STATUS", "0")
	t.Setenv("INSTALL_FIX_RUN_BINARIES", "1")
	return installConfig{
		bins: []binSpec{
			{name: "first", mainPkg: "./cmd/first", dir: installDir},
			{name: "second", mainPkg: "./cmd/second", dir: installDir},
		},
		distDir:            distDir,
		installPreCommand:  `sh "$INSTALL_FIX_PRE_HOOK"`,
		installPostCommand: `sh "$INSTALL_FIX_POST_HOOK"`,
	}, logPath
}

func installFixWriteScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func installFixAssertLog(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("hook log = %q, want %q", got, want)
	}
}

func installFixAssertAbsent(t *testing.T, cfg installConfig) {
	t.Helper()
	for _, bin := range cfg.bins {
		target := filepath.Join(bin.dir, bin.name)
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("installed binary %q: stat error = %v, want file absent", target, err)
		}
	}
}
