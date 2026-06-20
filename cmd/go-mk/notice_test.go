package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunNoticeRecordsDirectiveAsAppliedForFreshRepo(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)

	noticesPath := filepath.Join(root, "notices.txt")
	notices := strings.Join([]string{
		"1\tGATE=golangci LINTER=revive RULE=file-length-limit\tEnabled historical rule",
		"2\t-\tAnnouncement-only notice",
		"",
	}, "\n")
	if err := os.WriteFile(noticesPath, []byte(notices), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_MK_NOTICES_FILE", noticesPath)
	t.Setenv("GO_MK_APPLIED_NOTICES", ".go-mk-applied-notices")

	var status int
	stderr := captureStderr(t, func() {
		status = runNotice()
	})
	if status != 0 {
		t.Fatalf("runNotice status = %d, want 0", status)
	}

	applied, err := os.ReadFile(".go-mk-applied-notices")
	if err != nil {
		t.Fatalf("read applied notices: %v", err)
	}
	if string(applied) != "1\n" {
		t.Fatalf("applied notices = %q, want %q", string(applied), "1\n")
	}
	if _, err := os.Stat(".golangci-lint-baseline.txt"); !os.IsNotExist(err) {
		t.Fatalf(".golangci-lint-baseline.txt stat error = %v, want not exist", err)
	}
	seen, err := os.ReadFile(filepath.Join(makeDir, ".go-mk-notice-seen"))
	if err != nil {
		t.Fatalf("read seen file: %v", err)
	}
	if string(seen) != "2\n" {
		t.Fatalf("seen file = %q, want %q", string(seen), "2\n")
	}
	if strings.Contains(stderr, "notice #1") {
		t.Fatalf("stderr contains directive notice summary: %q", stderr)
	}
	if !strings.Contains(stderr, "notice #2: Announcement-only notice") {
		t.Fatalf("stderr = %q, want announcement notice", stderr)
	}
}

func TestAnyConfiguredBaselineFileExistsDetectsDefaultBaseline(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)

	if anyConfiguredBaselineFileExists() {
		t.Fatal("anyConfiguredBaselineFileExists = true before any baseline exists")
	}
	if err := os.WriteFile(".golangci-lint-baseline.txt", []byte("# baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !anyConfiguredBaselineFileExists() {
		t.Fatal("anyConfiguredBaselineFileExists = false after default baseline exists")
	}
}

func TestAnyConfiguredBaselineFileExistsUsesCustomBaselinePaths(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	t.Setenv("GOLANGCI_LINT_BASELINE", filepath.Join("custom", "golangci.txt"))
	t.Setenv("GOCYCLO_BASELINE", filepath.Join("custom", "gocyclo.txt"))
	t.Setenv("DEADCODE_BASELINE", filepath.Join("custom", "deadcode.txt"))
	t.Setenv("STATICCHECK_EXTRA_BASELINE", filepath.Join("custom", "staticcheck.txt"))

	if anyConfiguredBaselineFileExists() {
		t.Fatal("anyConfiguredBaselineFileExists = true before custom baselines exist")
	}
	if err := os.Mkdir("custom", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("custom", "staticcheck.txt"), []byte("# baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !anyConfiguredBaselineFileExists() {
		t.Fatal("anyConfiguredBaselineFileExists = false after custom baseline exists")
	}
}

func clearBaselineEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOLANGCI_LINT_BASELINE", "")
	t.Setenv("GOCYCLO_BASELINE", "")
	t.Setenv("DEADCODE_BASELINE", "")
	t.Setenv("STATICCHECK_EXTRA_BASELINE", "")
}

func chdir(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previous)
	})
}

func captureStderr(t *testing.T, action func()) string {
	t.Helper()
	previous := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	action()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = previous
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(output)
}
