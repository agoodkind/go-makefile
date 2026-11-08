package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
	"goodkind.io/go-makefile/internal/capture"
	"goodkind.io/go-makefile/internal/notice"
)

// TestNoticePreparesGoMakefileModules checks historical notices in both
// modules.
func TestNoticePreparesGoMakefileModules(t *testing.T) {
	t.Parallel()
	consumer, repository := newNoticeModules(t)
	previous := setPendingNotices(t, consumer, "2")
	setPendingNotices(t, filepath.Join(consumer, "staticcheck"), "2")

	output, err := runConsumerMake(
		consumer,
		fixtureStaticcheckGate,
		"GO_MK_LOCAL_ARGS=GO_MK_DEV_DIR="+repository+" GO_MK_BUILD_REPO="+repository,
		"STATICCHECK_EXTRA_LOCAL_ARGS=STATICCHECK_EXTRA_BUILD_REPO="+filepath.Join(repository, "staticcheck"),
		"_GO_MK_NOTICES_FILE="+filepath.Join(consumer, "notices.txt"),
		"STATICCHECK_EXTRA_FLAGS=-testpackage",
		fixtureTestFlagsOff,
	)
	if err != nil {
		t.Fatalf("go-makefile historical notice failed: %v\n%s", err, output)
	}

	for _, directory := range []string{consumer, filepath.Join(consumer, "staticcheck")} {
		if baseline := readConsumerFile(t, directory, baselineFileName); !strings.Contains(baseline, olderTestFinding) {
			t.Fatalf("baseline in %s lacks the historical finding:\n%s", directory, baseline)
		}

		if applied := readConsumerFile(t, directory, appliedNoticeFile); applied != previous+"2\n" {
			t.Fatalf("historical test notice did not apply in %s:\n%s", directory, applied)
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

	for _, name := range []string{fixtureMakefile, "go.mk"} {
		writeConsumerFile(t, consumer, name, readConsumerFile(t, repository, name))
	}

	writeConsumerFile(t, consumer, fixtureModule, "module goodkind.io/go-makefile\n\ngo 1.26\n")
	writeConsumerFile(t, consumer, "staticcheck/go.mod", "module goodkind.io/go-makefile/staticcheck\n\ngo 1.26\n")
	writeConsumerFile(t, consumer, "golangci.yml", readConsumerFile(t, consumer, ".make/golangci.yml"))

	for _, name := range []string{fixtureWidget, fixtureWidgetTest, ".make/golangci.yml", appliedNoticeFile} {
		writeConsumerFile(t, consumer, filepath.Join("staticcheck", name), readConsumerFile(t, consumer, name))
	}

	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Configure both go-makefile modules")

	return consumer, repository
}

// TestNoticeUsesCurrentConfigInHistoricalModule checks a nested module
// against the current policy.
func TestNoticeUsesCurrentConfigInHistoricalModule(t *testing.T) {
	t.Parallel()
	consumer, repository := newNoticeModules(t)
	module := filepath.Join(consumer, "staticcheck")
	const spacing = `package widget
func Spacing(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
`
	writeConsumerFile(t, module, "widget/spacing.go", spacing)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical spacing")
	writeConsumerFile(t, consumer, "golangci.yml", readConsumerFile(t, repository, "golangci.yml"))
	previous := setPendingNotices(t, module, whitespaceNotice)
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
		"_GO_MK_NOTICES_FILE="+filepath.Join(module, "notices.txt"),
	)
	if err != nil {
		t.Fatalf("capture historical whitespace: %v\n%s", err, output)
	}

	if applied := readConsumerFile(t, module, appliedNoticeFile); applied != previous+whitespaceNotice+"\n" {
		t.Fatalf("whitespace notice did not apply:\n%s\n%s", applied, output)
	}

	baseline := readConsumerFile(t, module, ".golangci-lint-baseline.txt")
	if !strings.Contains(baseline, "widget/spacing.go:") || !strings.Contains(baseline, "(wsl_v5)") {
		t.Fatalf("baseline lacks the historical spacing finding:\n%s\n%s", baseline, output)
	}
}

// TestHistoricalCaptureIsolatesGolangciCache checks the linter's cache
// and caller settings.
func TestHistoricalCaptureIsolatesGolangciCache(t *testing.T) {
	historical := newConsumerRepository(t, beforeNoticeDate)
	current := newConsumerRepository(t, beforeNoticeDate)
	callerCache := t.TempDir()
	engine := gomk.BuiltTestEngine(t)
	validation, err := template.ParseFiles(filepath.Join("testdata", "history", "engine.mk"))
	if err != nil {
		t.Fatal(err)
	}

	var makefile strings.Builder
	if err := validation.Execute(&makefile, engine); err != nil {
		t.Fatal(err)
	}

	makefile.WriteString(readConsumerFile(t, historical, fixtureMakefile))
	writeConsumerFile(t, historical, fixtureMakefile, makefile.String())
	t.Chdir(current)
	caller, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOLANGCI_LINT_CACHE", callerCache)
	t.Setenv("GOLANGCI_LINT", "golangci-lint")
	t.Setenv("GO_MK_NOTICE_MAKEFILE", fixtureMakefile)
	const flags = "-c .make/golangci.yml"
	t.Setenv("GOLANGCI_LINT_FLAGS", flags)
	t.Setenv("GOLANGCI_LINT_RUN_FLAGS", flags+" --enable-only=wsl_v5")
	t.Setenv("_GO_MK_ROOT", caller)
	var historicalCache string

	outputPath := filepath.Join(t.TempDir(), "golangci-lint.out")
	err = notice.CaptureWithEngine(engine, historical, outputPath, "", func(rawPath, _ string) error {
		historicalCache = os.Getenv("GOLANGCI_LINT_CACHE")

		for _, name := range []string{"GOLANGCI_LINT_FLAGS", "GOLANGCI_LINT_RUN_FLAGS"} {
			path := strings.Fields(os.Getenv(name))[1]
			parent, err := filepath.EvalSymlinks(filepath.Join(historical, ".make"))
			if err != nil || filepath.Dir(path) != parent {
				t.Fatalf("%s historical config %s must use %s: %v", name, path, parent, err)
			}
		}

		arguments := append([]string{"run"}, strings.Fields(os.Getenv("GOLANGCI_LINT_RUN_FLAGS"))...)
		arguments = append(arguments, "./widget")
		result, err := capture.Run(os.Getenv("GOLANGCI_LINT"), arguments, os.Environ(), rawPath)
		if err != nil || result.Status != 0 {
			t.Fatalf("historical linter exited %d: %v; output: %s", result.Status, err, rawPath)
		}

		return nil
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
		"GO_MK_NOTICE_MAKEFILE": fixtureMakefile,
	} {
		if value := os.Getenv(name); value != expected {
			t.Fatalf("%s after historical capture = %q, want %q", name, value, expected)
		}
	}
}
