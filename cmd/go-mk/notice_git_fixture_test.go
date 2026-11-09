package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

func TestRunNoticeRecordsDirectiveWhenAdoptionIsAfterNotice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git fixture uses /dev/null")
	}

	environment := noticeGitEnvironment(t)
	engine := gomk.BuiltTestEngine(t)
	root := noticeGitRepository(t, environment, "2026-05-26T03:38:46Z")
	baselinePath := filepath.Join(root, ".golangci-lint-baseline.txt")
	if err := os.WriteFile(baselinePath, []byte("# existing baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	noticesPath := filepath.Join(root, "notices.txt")
	directive := "INTRODUCED=2026-05-25T19:38:46-07:00 GATE=golangci LINTER=revive RULE=file-length-limit"
	if err := os.WriteFile(
		noticesPath,
		[]byte("1\t"+directive+"\tEnabled historical rule\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(engine, "notice")
	command.Dir = root
	command.Env = append(
		environment,
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

func TestLegacyMakefileNoticePreservesEffectiveDate(t *testing.T) {
	engine := gomk.BuiltTestEngine(t)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	for _, scenario := range []struct {
		name       string
		fixture    string
		fresh      bool
		historical bool
	}{
		{"legacy include", "legacy", false, true},
		{"inline bootstrap", "inline", false, true},
		{"fresh adoption", "comments", true, false},
		{"comment mentions", "comments", false, false},
		{"unrelated URL", "unrelated", false, false},
		{"unused URL", "unused", false, false},
		{"unrelated fetch target", "documentation", false, false},
		{"unevaluated definition", "definitions", false, false},
		{"inactive conditional", "conditional", false, false},
	} {
		t.Run(
			scenario.name,
			func(t *testing.T) {
				consumer := legacyNoticeRepository(t, scenario.fixture)
				if scenario.fresh {
					writeLegacyMakefile(t, consumer, "legacy")
				}

				writeConsumerFile(
					t,
					consumer,
					"widget/later_test.go",
					strings.Replace(assertionFreeTest, "TestDoubleRuns", "TestLater", 1),
				)
				runConsumerGit(t, consumer, afterNoticeDate, "add", ".")
				runConsumerGit(
					t,
					consumer,
					afterNoticeDate,
					"commit",
					"--quiet",
					"--message",
					"Add a finding after the notice",
				)
				writeConsumerFile(
					t,
					consumer,
					".make/notices.txt",
					"2\tINTRODUCED=2026-10-05T00:00:00-07:00 GATE=staticcheck-extra PATTERN=_test\\.go:\tCheck existing tests\n",
				)
				output, gateErr := runConsumerMake(
					consumer,
					"-f",
					filepath.Join(repository, "go.mk"),
					"GO_MK_DEV_DIR="+repository,
					"GO_MK_BIN="+engine,
					"STATICCHECK_EXTRA_BUILD_REPO="+filepath.Join(repository, "staticcheck"),
					"STATICCHECK_EXTRA_BUILD_PKG=./cmd/staticcheck-extra",
					"_GO_MK_NOTICES_FILE="+filepath.Join(consumer, ".make/notices.txt"),
					"GO_MK_NOTICE_MAKEFILE="+filepath.Join(repository, "go.mk"),
					"staticcheck-extra",
					testAnalyzerFlags,
				)
				if gateErr == nil || !strings.Contains(output, "later_test.go") {
					t.Fatalf("gate did not reject the later finding: %v\n%s", gateErr, output)
				}

				baseline, readErr := os.ReadFile(filepath.Join(consumer, baselineFileName))
				if !scenario.historical {
					if !os.IsNotExist(readErr) {
						t.Fatalf(
							"fresh consumer baseline read error = %v; baseline = %s",
							readErr,
							baseline,
						)
					}

					return
				}

				if readErr != nil {
					t.Fatalf("historical baseline read failed: %v\n%s", readErr, output)
				}

				if !strings.Contains(string(baseline), "widget/widget_test.go") ||
					strings.Contains(string(baseline), "later_test.go") {
					t.Fatalf(
						"baseline did not preserve the effective date:\n%s\n%s",
						baseline,
						output,
					)
				}
			},
		)
	}
}

func legacyNoticeRepository(t *testing.T, fixture string) string {
	t.Helper()
	adoptionDate := "2026-05-03T11:15:05-07:00"
	if fixture == "inline" {
		adoptionDate = "2026-05-24T20:39:49-07:00"
	}

	consumer := t.TempDir()
	writeConsumerFile(t, consumer, "go.mod", "module example.com/consumer\n\ngo 1.26\n")
	writeConsumerFile(t, consumer, ".gitignore", ".make/\n")
	writeConsumerFile(
		t,
		consumer,
		"widget/widget.go",
		"package widget\n\nfunc Double(value int) int { return value * 2 }\n",
	)
	writeConsumerFile(t, consumer, "widget/widget_test.go", assertionFreeTest)
	writeLegacyMakefile(t, consumer, fixture)
	runConsumerGit(t, consumer, adoptionDate, "init", "--quiet", "--initial-branch=main")
	runConsumerGit(t, consumer, adoptionDate, "add", ".")
	runConsumerGit(
		t,
		consumer,
		adoptionDate,
		"commit",
		"--quiet",
		"--message",
		"Add the consumer source",
	)

	return consumer
}

func writeLegacyMakefile(t *testing.T, consumer, fixture string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "adoption", fixture+".mk"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, "Makefile", string(contents))
}

func TestRunNoticeLeavesDirectiveUnappliedWhenGitHistoryIsBroken(t *testing.T) {
	engine := gomk.BuiltTestEngine(t)
	consumer := legacyNoticeRepository(t, "legacy")
	writeConsumerFile(t, consumer, ".git/HEAD", strings.Repeat("0", 40)+"\n")
	notices := filepath.Join(consumer, "notices.txt")
	writeConsumerFile(
		t,
		consumer,
		"notices.txt",
		"2\tINTRODUCED=2026-10-05T00:00:00-07:00 GATE=staticcheck-extra PATTERN=_test\\.go:\tCheck existing tests\n",
	)
	command := exec.CommandContext(t.Context(), engine, "notice")
	command.Dir = consumer
	command.Env = append(
		noticeGitEnvironment(t),
		"_GO_MK_ROOT="+consumer,
		"_GO_MK_NOTICES_FILE="+notices,
		"GO_MK_APPLIED_NOTICES="+appliedNoticeFile,
		"GOPROXY=off",
		"GOSUMDB=off",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("notice command failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "adoption history could not be read") {
		t.Fatalf("notice did not report the Git history failure:\n%s", output)
	}

	for _, name := range []string{appliedNoticeFile, baselineFileName} {
		if _, statErr := os.Stat(filepath.Join(consumer, name)); !os.IsNotExist(statErr) {
			t.Fatalf("artifact %s stat error = %v after a history failure", name, statErr)
		}
	}
}

func noticeGitEnvironment(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	globalConfig := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	return gomk.IsolatedTestEnvironment(
		home,
		map[string]string{
			"XDG_CONFIG_HOME":     home,
			"GIT_AUTHOR_NAME":     "Notice test",
			"GIT_AUTHOR_EMAIL":    "notice@example.invalid",
			"GIT_COMMITTER_NAME":  "Notice test",
			"GIT_COMMITTER_EMAIL": "notice@example.invalid",
			"GOPATH":              filepath.Join(home, "go"),
		},
	)
}

func noticeGitRepository(t *testing.T, environment []string, adoptionDate string) string {
	t.Helper()
	root := t.TempDir()
	environment = append(
		environment,
		"GIT_AUTHOR_DATE="+adoptionDate,
		"GIT_COMMITTER_DATE="+adoptionDate,
	)
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
	noticeGitCommand(
		t,
		root,
		environment,
		"-c",
		"commit.gpgsign=false",
		"commit",
		"--quiet",
		"-m",
		"Add consumer module",
	)

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
