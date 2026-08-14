package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDirective = "INTRODUCED=2026-05-25T19:38:46-07:00 GATE=golangci LINTER=revive RULE=file-length-limit"

var testIntroducedTime = time.Date(2026, 5, 25, 19, 38, 46, 0, time.FixedZone("PDT", -7*60*60))

func TestRunNoticeRecordsDirectiveAsAppliedForFreshRepo(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)
	stubNoticeAdoptionTime(t, time.Time{}, false)
	forbidNoticeBaseline(t)

	noticesPath := filepath.Join(root, "notices.txt")
	notices := strings.Join([]string{
		"1\t" + testDirective + "\tEnabled historical rule",
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

func TestRunNoticeShowsDirectiveWhenFreshAppliedRecordFails(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)
	stubNoticeAdoptionTime(t, time.Time{}, false)
	forbidNoticeBaseline(t)

	noticesPath := filepath.Join(root, "notices.txt")
	notices := "1\t" + testDirective + "\tEnabled historical rule\n"
	if err := os.WriteFile(noticesPath, []byte(notices), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("not-a-directory", []byte("file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_MK_NOTICES_FILE", noticesPath)
	t.Setenv("GO_MK_APPLIED_NOTICES", filepath.Join("not-a-directory", ".go-mk-applied-notices"))

	var status int
	stderr := captureStderr(t, func() {
		status = runNotice()
	})
	if status != 0 {
		t.Fatalf("runNotice status = %d, want 0", status)
	}
	if !strings.Contains(stderr, "could not record applied notice") {
		t.Fatalf("stderr = %q, want applied-recording diagnostic", stderr)
	}
	if !strings.Contains(stderr, "notice #1: Enabled historical rule") {
		t.Fatalf("stderr = %q, want directive summary when recording fails", stderr)
	}
	if _, err := os.Stat(".golangci-lint-baseline.txt"); !os.IsNotExist(err) {
		t.Fatalf(".golangci-lint-baseline.txt stat error = %v, want not exist", err)
	}
}

func TestRunNoticeCreatesFreshAppliedNoticeParentDirectory(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)
	stubNoticeAdoptionTime(t, time.Time{}, false)
	forbidNoticeBaseline(t)

	noticesPath := filepath.Join(root, "notices.txt")
	notices := "1\t" + testDirective + "\tEnabled historical rule\n"
	if err := os.WriteFile(noticesPath, []byte(notices), 0o644); err != nil {
		t.Fatal(err)
	}
	appliedPath := filepath.Join(".make", "nested", ".go-mk-applied-notices")
	t.Setenv("GO_MK_NOTICES_FILE", noticesPath)
	t.Setenv("GO_MK_APPLIED_NOTICES", appliedPath)

	var status int
	stderr := captureStderr(t, func() {
		status = runNotice()
	})
	if status != 0 {
		t.Fatalf("runNotice status = %d, want 0", status)
	}
	if strings.Contains(stderr, "go-makefile notice #1:") {
		t.Fatalf("stderr = %q, want no directive notice output after applied record succeeds", stderr)
	}
	applied, err := os.ReadFile(appliedPath)
	if err != nil {
		t.Fatalf("read applied notices: %v", err)
	}
	if string(applied) != "1\n" {
		t.Fatalf("applied notices = %q, want %q", string(applied), "1\n")
	}
}

func TestRunNoticeIgnoresEmptyBaselineFilesWhenHistoryIsUnavailable(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)
	stubNoticeAdoptionTime(t, time.Time{}, false)
	forbidNoticeBaseline(t)

	if err := os.WriteFile(".golangci-lint-baseline.txt", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	noticesPath := filepath.Join(root, "notices.txt")
	notices := "1\t" + testDirective + "\tEnabled historical rule\n"
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
	if strings.Contains(stderr, "auto-baselining") {
		t.Fatalf("stderr = %q, want no auto-baseline output", stderr)
	}
	assertNoticeAppliedFile(t, ".go-mk-applied-notices", "1\n")
	assertFileText(t, ".golangci-lint-baseline.txt", "")
}

func TestRunNoticeRecordsDirectiveWhenAdoptionIsAfterNotice(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)
	stubNoticeAdoptionTime(t, testIntroducedTime.Add(time.Hour), true)
	forbidNoticeBaseline(t)

	if err := os.WriteFile(".golangci-lint-baseline.txt", []byte("# existing baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	noticesPath := filepath.Join(root, "notices.txt")
	notices := "1\t" + testDirective + "\tEnabled historical rule\n"
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
	if strings.Contains(stderr, "auto-baselining") {
		t.Fatalf("stderr = %q, want no auto-baseline output", stderr)
	}
	assertNoticeAppliedFile(t, ".go-mk-applied-notices", "1\n")
	assertFileText(t, ".golangci-lint-baseline.txt", "# existing baseline\n")
}

func TestRunNoticeAutoBaselinesWhenAdoptionIsBeforeNotice(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)
	stubNoticeAdoptionTime(t, testIntroducedTime.Add(-time.Hour), true)
	baselineCalls := recordNoticeBaselineCalls(t)

	noticesPath := filepath.Join(root, "notices.txt")
	notices := "1\t" + testDirective + "\tEnabled historical rule\n"
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
	if len(*baselineCalls) != 1 {
		t.Fatalf("baseline call count = %d, want 1", len(*baselineCalls))
	}
	if (*baselineCalls)[0] != "auto-baseline-scope" {
		t.Fatalf("baseline calls = %#v, want auto-baseline-scope", *baselineCalls)
	}
	if !strings.Contains(stderr, "auto-baselining existing findings") {
		t.Fatalf("stderr = %q, want auto-baseline output", stderr)
	}
	assertNoticeAppliedFile(t, ".go-mk-applied-notices", "1\n")
}

func TestAnyConfiguredBaselineFileHasContentDetectsDefaultBaseline(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)

	if anyConfiguredBaselineFileHasContent() {
		t.Fatal("anyConfiguredBaselineFileHasContent = true before any baseline exists")
	}
	if err := os.WriteFile(".golangci-lint-baseline.txt", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if anyConfiguredBaselineFileHasContent() {
		t.Fatal("anyConfiguredBaselineFileHasContent = true after empty baseline exists")
	}
	if err := os.WriteFile(".golangci-lint-baseline.txt", []byte("# baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !anyConfiguredBaselineFileHasContent() {
		t.Fatal("anyConfiguredBaselineFileHasContent = false after default baseline has content")
	}
}

func TestAnyConfiguredBaselineFileHasContentUsesCustomBaselinePaths(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	t.Setenv("GOLANGCI_LINT_BASELINE", filepath.Join("custom", "golangci.txt"))
	t.Setenv("GOCYCLO_BASELINE", filepath.Join("custom", "gocyclo.txt"))
	t.Setenv("DEADCODE_BASELINE", filepath.Join("custom", "deadcode.txt"))
	t.Setenv("STATICCHECK_EXTRA_BASELINE", filepath.Join("custom", "staticcheck.txt"))

	if anyConfiguredBaselineFileHasContent() {
		t.Fatal("anyConfiguredBaselineFileHasContent = true before custom baselines exist")
	}
	if err := os.Mkdir("custom", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("custom", "staticcheck.txt"), []byte("# baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !anyConfiguredBaselineFileHasContent() {
		t.Fatal("anyConfiguredBaselineFileHasContent = false after custom baseline has content")
	}
}

func TestAnyConfiguredBaselineFileHasContentTreatsStatErrorsAsExisting(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root can bypass directory permissions")
	}
	root := t.TempDir()
	chdir(t, root)
	clearBaselineEnv(t)

	blockedDirectory := filepath.Join(root, "blocked")
	if err := os.Mkdir(blockedDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	baselinePath := filepath.Join(blockedDirectory, "golangci.txt")
	if err := os.WriteFile(baselinePath, []byte("# baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blockedDirectory, 0o755); err != nil {
			t.Fatalf("restore blocked directory permissions: %v", err)
		}
	})
	if err := os.Chmod(blockedDirectory, 0); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOLANGCI_LINT_BASELINE", baselinePath)

	if !anyConfiguredBaselineFileHasContent() {
		t.Fatal("anyConfiguredBaselineFileHasContent = false for unreadable baseline path")
	}
}

func stubNoticeAdoptionTime(t *testing.T, adoptionTime time.Time, ok bool) {
	t.Helper()
	previous := noticeAdoptionTimeFunc
	noticeAdoptionTimeFunc = func() (time.Time, bool) {
		return adoptionTime, ok
	}
	t.Cleanup(func() {
		noticeAdoptionTimeFunc = previous
	})
}

func forbidNoticeBaseline(t *testing.T) {
	t.Helper()
	previous := runNoticeBaselineFunc
	runNoticeBaselineFunc = func(args []string) int {
		t.Fatalf("runNoticeBaselineFunc called with %#v", args)
		return 1
	}
	t.Cleanup(func() {
		runNoticeBaselineFunc = previous
	})
}

func recordNoticeBaselineCalls(t *testing.T) *[]string {
	t.Helper()
	calls := []string{}
	previous := runNoticeBaselineFunc
	runNoticeBaselineFunc = func(args []string) int {
		if os.Getenv("LINTER") != "revive" {
			t.Fatalf("LINTER = %q, want revive", os.Getenv("LINTER"))
		}
		if os.Getenv("RULE") != "file-length-limit" {
			t.Fatalf("RULE = %q, want file-length-limit", os.Getenv("RULE"))
		}
		calls = append(calls, strings.Join(args, " "))
		return 0
	}
	t.Cleanup(func() {
		runNoticeBaselineFunc = previous
	})
	return &calls
}

func assertNoticeAppliedFile(t *testing.T, filePath string, expected string) {
	t.Helper()
	applied, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read applied notices: %v", err)
	}
	if string(applied) != expected {
		t.Fatalf("applied notices = %q, want %q", string(applied), expected)
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
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}

func captureStderr(t *testing.T, action func()) string {
	t.Helper()
	previous := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	writerClosed := false
	defer func() {
		os.Stderr = previous
		if !writerClosed {
			_ = writer.Close()
		}
		_ = reader.Close()
	}()
	os.Stderr = writer
	action()
	os.Stderr = previous
	closeErr := writer.Close()
	writerClosed = true
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}
