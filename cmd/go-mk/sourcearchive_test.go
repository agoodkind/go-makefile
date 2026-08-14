package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripShellFullLineCommentsPreservesShebangAndQuotedHash(t *testing.T) {
	input := []byte("#!/usr/bin/env bash\n# full line\nprintf '# not a comment\\n'\n")
	got := string(stripShellFullLineComments(input))
	if !strings.HasPrefix(got, "#!/usr/bin/env bash\n") {
		t.Fatalf("shebang missing: %q", got)
	}
	if strings.Contains(got, "# full line") {
		t.Fatalf("full-line comment remained: %q", got)
	}
	if !strings.Contains(got, "printf '# not a comment") {
		t.Fatalf("quoted hash stripped: %q", got)
	}
	if err := validateShellSyntax("test.sh", []byte(got)); err != nil {
		t.Fatalf("bash -n failed: %v", err)
	}
}

func TestWriteSourceArchiveExtractsGoMkWithSystemTar(t *testing.T) {
	repoRoot := repoRootForTest(t)
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir repo root: %v", err)
	}

	distDir := t.TempDir()
	archivePath, err := writeSourceArchive(distDir)
	if err != nil {
		t.Fatalf("writeSourceArchive: %v", err)
	}

	extractDir := t.TempDir()
	cmd := exec.Command("tar", "-xzf", archivePath, "-C", extractDir, "--strip-components", "1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar extract: %v\n%s", err, output)
	}
	goMkPath := filepath.Join(extractDir, "go.mk")
	if _, err := os.Stat(goMkPath); err != nil {
		t.Fatalf("go.mk missing after extract: %v", err)
	}
}

func TestAppendSourceArchiveIfEnabledSkipsByDefault(t *testing.T) {
	archives := []string{"dist/go-mk_linux_amd64.tar.gz"}
	got, err := appendSourceArchiveIfEnabled(t.TempDir(), archives)
	if err != nil {
		t.Fatalf("appendSourceArchiveIfEnabled: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("archives = %#v, want unchanged single entry", got)
	}
}

func TestAppendSourceArchiveIfEnabledWritesArchive(t *testing.T) {
	repoRoot := repoRootForTest(t)
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir repo root: %v", err)
	}
	t.Setenv("RELEASE_SOURCE_ARCHIVE", "1")

	distDir := t.TempDir()
	got, err := appendSourceArchiveIfEnabled(distDir, nil)
	if err != nil {
		t.Fatalf("appendSourceArchiveIfEnabled: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("archives = %#v, want one source archive", got)
	}
	if filepath.Base(got[0]) != sourceArchiveName {
		t.Fatalf("archive = %q, want %q", got[0], sourceArchiveName)
	}
}
