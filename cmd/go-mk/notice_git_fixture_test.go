package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunNoticeRecordsDirectiveWhenAdoptionIsAfterNotice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git fixture uses /dev/null")
	}
	environment := noticeGitEnvironment(t)
	engine := noticeGitBuildEngine(t, environment)
	root := noticeGitRepository(t, environment, "2026-05-26T03:38:46Z")
	baselinePath := filepath.Join(root, ".golangci-lint-baseline.txt")
	if err := os.WriteFile(baselinePath, []byte("# existing baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	noticesPath := filepath.Join(root, "notices.txt")
	directive := "INTRODUCED=2026-05-25T19:38:46-07:00 GATE=golangci LINTER=revive RULE=file-length-limit"
	if err := os.WriteFile(noticesPath, []byte("1\t"+directive+"\tEnabled historical rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(engine, "notice")
	command.Dir = root
	command.Env = append(environment,
		"_GO_MK_NOTICES_FILE="+noticesPath,
		"GO_MK_APPLIED_NOTICES=.go-mk-applied-notices",
		"GOPROXY=off",
		"GOSUMDB=off",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go-mk notice: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "auto-baselining") {
		t.Fatalf("notice output = %q, want no auto-baseline output", output)
	}
	noticeGitAssertText(t, filepath.Join(root, ".go-mk-applied-notices"), "1\n")
	noticeGitAssertText(t, baselinePath, "# existing baseline\n")
}

func noticeGitEnvironment(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	globalConfig := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + home,
		"XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
		"GIT_CONFIG_GLOBAL=" + globalConfig,
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Notice test",
		"GIT_AUTHOR_EMAIL=notice@example.invalid",
		"GIT_COMMITTER_NAME=Notice test",
		"GIT_COMMITTER_EMAIL=notice@example.invalid",
		"GOPATH=" + filepath.Join(home, "go"),
		"GOCACHE=" + filepath.Join(home, "go-build"),
		"GOMODCACHE=" + filepath.Join(home, "go-mod"),
		"GOENV=off",
		"GOFLAGS=-modcacherw",
		"GOTOOLCHAIN=local",
		"CGO_ENABLED=0",
	}
}

func noticeGitBuildEngine(t *testing.T, environment []string) string {
	t.Helper()
	engine := filepath.Join(t.TempDir(), "go-mk")
	command := exec.Command("go", "build", "-o", engine, ".")
	command.Env = append(environment, "GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build go-mk: %v\n%s", err, output)
	}
	return engine
}

func noticeGitRepository(t *testing.T, environment []string, adoptionDate string) string {
	t.Helper()
	root := t.TempDir()
	environment = append(environment, "GIT_AUTHOR_DATE="+adoptionDate, "GIT_COMMITTER_DATE="+adoptionDate)
	noticeGitCommand(t, root, environment, "init", "--quiet")
	for name, contents := range map[string]string{
		"go.mod":       "module example.invalid/notice\n\ngo 1.24\n",
		"bootstrap.mk": "# The consumer includes go-makefile.\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The adoption detector reads bootstrap.mk rather than go.mod.
	noticeGitCommand(t, root, environment, "add", "go.mod", "bootstrap.mk")
	noticeGitCommand(t, root, environment, "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Add consumer module")
	return root
}

func noticeGitCommand(t *testing.T, root string, environment []string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func noticeGitAssertText(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(content) != expected {
		t.Fatalf("%s = %q, want %q", path, content, expected)
	}
}
