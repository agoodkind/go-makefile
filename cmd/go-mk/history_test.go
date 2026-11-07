package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"goodkind.io/go-makefile/internal/notice"
)

func TestNoticePreparesGoMakefileModules(t *testing.T) {
	t.Parallel()
	consumer, repository := newNoticeModules(t)

	output, err := runConsumerMake(
		consumer,
		"staticcheck-extra",
		"GO_MK_LOCAL_ARGS=GO_MK_DEV_DIR="+repository+" GO_MK_BUILD_REPO="+repository,
		"STATICCHECK_EXTRA_LOCAL_ARGS=STATICCHECK_EXTRA_BUILD_REPO="+filepath.Join(repository, "staticcheck"),
		"_GO_MK_NOTICES_FILE="+filepath.Join(consumer, "notices.txt"),
		"STATICCHECK_EXTRA_FLAGS=-testpackage",
		"STATICCHECK_EXTRA_TEST_FLAGS=",
	)
	if err != nil {
		t.Fatalf("go-makefile historical notice failed: %v\n%s", err, output)
	}

	for _, directory := range []string{consumer, filepath.Join(consumer, "staticcheck")} {
		if baseline := readConsumerFile(t, directory, baselineFileName); !strings.Contains(baseline, olderTestFinding) {
			t.Fatalf("baseline in %s lacks the historical finding:\n%s", directory, baseline)
		}

		if applied := readConsumerFile(t, directory, appliedNoticeFile); applied != "1\n2\n" {
			t.Fatalf("applied notices in %s = %q, want both notices", directory, applied)
		}
	}
}

func newNoticeModules(t *testing.T) (string, string) {
	t.Helper()
	consumer := newConsumerRepository(t, beforeNoticeDate)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Makefile", "go.mk"} {
		writeConsumerFile(t, consumer, name, readConsumerFile(t, repository, name))
	}

	writeConsumerFile(t, consumer, "go.mod", "module goodkind.io/go-makefile\n\ngo 1.26\n")
	writeConsumerFile(t, consumer, "staticcheck/go.mod", "module goodkind.io/go-makefile/staticcheck\n\ngo 1.26\n")
	writeConsumerFile(t, consumer, "golangci.yml", readConsumerFile(t, consumer, ".make/golangci.yml"))

	for _, name := range []string{"widget/widget.go", "widget/widget_test.go", ".make/golangci.yml", appliedNoticeFile} {
		writeConsumerFile(t, consumer, filepath.Join("staticcheck", name), readConsumerFile(t, consumer, name))
	}

	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Configure both go-makefile modules")

	return consumer, repository
}

func TestNoticeUsesCurrentConfigInHistoricalModule(t *testing.T) {
	t.Parallel()
	consumer, repository := newNoticeModules(t)
	module := filepath.Join(consumer, "staticcheck")
	writeConsumerFile(t, module, "widget/spacing.go", "package widget\nfunc Spacing(value int) int {\nif value < 0 {\nreturn 0\n}\nreturn value\n}\n")
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical spacing")
	writeConsumerFile(t, consumer, "golangci.yml", readConsumerFile(t, repository, "golangci.yml"))
	writeConsumerFile(t, consumer, "notices.txt", readConsumerFile(t, repository, "notices.txt"))
	writeConsumerFile(t, module, appliedNoticeFile, "1\n2\n3\n4\n5\n")
	runConsumerGit(t, consumer, afterNoticeDate, "add", ".")
	runConsumerGit(t, consumer, afterNoticeDate, "commit", "--quiet", "--message", "Enable current whitespace policy")

	output, err := runConsumerMake(
		module,
		"-f",
		"../go.mk",
		"go-mk-notice",
		"GO_MK_DEV_DIR="+repository,
		"GOLANGCI_LINT_FLAGS=-c "+filepath.Join(consumer, "golangci.yml"),
		"GOLANGCI_LINT_RUN_FLAGS=--config=../golangci.yml --enable-only=wsl_v5",
		"_GO_MK_NOTICES_FILE="+filepath.Join(consumer, "notices.txt"),
	)
	if err != nil {
		t.Fatalf("capture historical whitespace: %v\n%s", err, output)
	}

	if applied := readConsumerFile(t, module, appliedNoticeFile); !strings.Contains(applied, "\n6\n") {
		t.Fatalf("notice 6 did not apply:\n%s\n%s", applied, output)
	}

	if baseline := readConsumerFile(t, module, ".golangci-lint-baseline.txt"); !strings.Contains(baseline, "widget/spacing.go:") || !strings.Contains(baseline, "(wsl_v5)") {
		t.Fatalf("baseline lacks the historical spacing finding:\n%s\n%s", baseline, output)
	}
}

func TestHistoricalCaptureIsolatesGolangciCache(t *testing.T) {
	historical := newConsumerRepository(t, beforeNoticeDate)
	current := newConsumerRepository(t, beforeNoticeDate)
	callerCache := t.TempDir()
	engine := noticeGitBuildEngine(t, os.Environ())
	validation, err := template.ParseFiles(filepath.Join("testdata", "history", "engine.mk"))
	if err != nil {
		t.Fatal(err)
	}

	var makefile strings.Builder
	if err := validation.Execute(&makefile, engine); err != nil {
		t.Fatal(err)
	}

	makefile.WriteString(readConsumerFile(t, historical, "Makefile"))
	writeConsumerFile(t, historical, "Makefile", makefile.String())
	t.Chdir(current)
	caller, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOLANGCI_LINT_CACHE", callerCache)
	t.Setenv("GOLANGCI_LINT", "golangci-lint")
	const flags = "-c .make/golangci.yml"
	t.Setenv("GOLANGCI_LINT_FLAGS", flags)
	t.Setenv("GOLANGCI_LINT_RUN_FLAGS", flags+" --enable-only=wsl_v5")
	t.Setenv("_GO_MK_ROOT", caller)
	var historicalCache string

	err = notice.CaptureWithEngine(engine, historical, "", "", func(_, _ string) error {
		historicalCache = os.Getenv("GOLANGCI_LINT_CACHE")

		for _, name := range []string{"GOLANGCI_LINT_FLAGS", "GOLANGCI_LINT_RUN_FLAGS"} {
			path := strings.Fields(os.Getenv(name))[1]
			parent, err := filepath.EvalSymlinks(filepath.Join(historical, ".make"))
			if err != nil || filepath.Dir(path) != parent {
				t.Fatalf("%s historical config %s must use %s: %v", name, path, parent, err)
			}
		}

		arguments := append([]string{"run"}, strings.Fields(os.Getenv("GOLANGCI_LINT_RUN_FLAGS"))...)
		command := exec.CommandContext(t.Context(), os.Getenv("GOLANGCI_LINT"), append(arguments, "./widget")...)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr

		return command.Run()
	})
	if err != nil {
		t.Fatal(err)
	}

	relative, err := filepath.Rel(historical, historicalCache)
	if err != nil || !filepath.IsAbs(historicalCache) || !filepath.IsLocal(relative) {
		t.Fatalf("historical lint cache %q is outside %q: %v", historicalCache, historical, err)
	}

	entries, err := os.ReadDir(historicalCache)
	if err != nil || len(entries) == 0 {
		t.Fatalf("golangci-lint did not write to the historical cache: %v", err)
	}

	entries, err = os.ReadDir(callerCache)
	if err != nil || len(entries) != 0 {
		t.Fatalf("historical lint changed the caller cache: %v, %v", entries, err)
	}

	directory, err := os.Getwd()
	if err != nil || directory != caller {
		t.Fatalf("working directory after historical capture = %q, want %q: %v", directory, caller, err)
	}

	for name, expected := range map[string]string{
		"GOLANGCI_LINT_CACHE": callerCache, "GOLANGCI_LINT": "golangci-lint", "_GO_MK_ROOT": caller,
		"GOLANGCI_LINT_FLAGS": flags, "GOLANGCI_LINT_RUN_FLAGS": flags + " --enable-only=wsl_v5",
	} {
		if value := os.Getenv(name); value != expected {
			t.Fatalf("%s after historical capture = %q, want %q", name, value, expected)
		}
	}
}
