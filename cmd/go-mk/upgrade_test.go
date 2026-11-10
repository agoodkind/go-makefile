package main_test

import (
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

func TestUpgradeNoticesPreservePassingConsumerBuild(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	engine := gomk.BuiltTestEngine(t)
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	original := upgradeFixture(t, "widget.go")
	writeConsumerFile(t, consumer, "widget/widget.go", original)
	writeConsumerFile(t, consumer, ".make/upgrade.yml", upgradeFixture(t, "legacy.yml"))
	writeConsumerFile(t, consumer, ".make/notices.txt", "")
	runConsumerGit(t, consumer, afterNoticeDate, "add", "widget/widget.go")
	runConsumerGit(t, consumer, afterNoticeDate, "commit", "-qm", "Record the passing consumer")
	arguments := []string{
		"build",
		"GO_MK_BIN=" + engine,
		"_GO_MK_NOTICES_FILE=" + filepath.Join(consumer, ".make/notices.txt"),
		"GO_MK_NOTICE_MAKEFILE=" + filepath.Join(repository, "go.mk"),
		"GO_MK_PLATFORMS=" + runtime.GOOS + "/" + runtime.GOARCH,
		"GOLANGCI_LINT_FLAGS=-c .make/upgrade.yml",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/upgrade.yml",
		"STATICCHECK_EXTRA_BUILD_REPO=" + filepath.Join(repository, "staticcheck"),
		"STATICCHECK_EXTRA_BUILD_PKG=./cmd/staticcheck-extra",
	}
	if output, err := runConsumerMake(consumer, arguments...); err != nil {
		t.Fatalf("consumer did not pass before upgrade: %v\n%s", err, output)
	}

	writeConsumerFile(t, consumer, ".make/upgrade.yml", upgradeFixture(t, "current.yml"))
	registry, err := os.ReadFile(filepath.Join(repository, "notices.txt"))
	if err != nil {
		t.Fatal(err)
	}

	var notices strings.Builder

	for _, line := range strings.Split(string(registry), "\n") {
		if strings.HasPrefix(line, "21\t") || strings.HasPrefix(line, "22\t") {
			notices.WriteString(line + "\n")
		}
	}

	writeConsumerFile(t, consumer, ".make/notices.txt", notices.String())
	if output, err := runConsumerMake(consumer, arguments...); err != nil {
		t.Fatalf("applying notices did not preserve the passing build: %v\n%s", err, output)
	}

	formatted := readConsumerFile(t, consumer, "widget/widget.go")
	if !slices.Equal(upgradeTokens(t, original), upgradeTokens(t, formatted)) {
		t.Fatal("applying notices changed the consumer's code tokens")
	}

	baseline := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt")
	if !strings.Contains(baseline, "Function 'Double' is too long") {
		t.Fatalf("formatting-induced size finding was not baselined:\n%s", baseline)
	}

	if !strings.Contains(baseline, "file-length-limit:") {
		t.Fatalf("formatting-induced file size finding was not baselined:\n%s", baseline)
	}

	applied := readConsumerFile(t, consumer, appliedNoticeFile)
	if !strings.Contains(applied, "21\n") || !strings.Contains(applied, "22\n") {
		t.Fatalf("successful upgrade notices were not recorded: %q", applied)
	}

	start := strings.Index(formatted, "func Double(")
	later := "package widget\n\n" + strings.Replace(
		formatted[start:],
		"func Double(",
		"func Later(",
		1,
	)
	writeConsumerFile(t, consumer, "widget/later.go", later)
	runConsumerGit(t, consumer, afterNoticeDate, "add", "widget/later.go")
	runConsumerGit(t, consumer, afterNoticeDate, "commit", "-qm", "Add a later violation")
	output, err := runConsumerMake(consumer, arguments...)
	if err == nil || !strings.Contains(output, "Function 'Later' is too long") {
		t.Fatalf("upgrade baseline accepted a later violation: %v\n%s", err, output)
	}

	if readConsumerFile(t, consumer, ".golangci-lint-baseline.txt") != baseline ||
		readConsumerFile(t, consumer, appliedNoticeFile) != applied {
		t.Fatal("the later build rewrote the recorded upgrade baseline or notice IDs")
	}
}

func upgradeFixture(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "upgrade", name))
	if err != nil {
		t.Fatal(err)
	}

	return string(contents)
}

func TestUpgradeNoticeRejectsUncommittedViolation(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)
	engine := gomk.BuiltTestEngine(t)
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	original := upgradeFixture(t, "widget.go")
	writeConsumerFile(t, consumer, "widget/widget.go", original)
	runConsumerGit(t, consumer, afterNoticeDate, "add", "widget/widget.go")
	runConsumerGit(
		t,
		consumer,
		afterNoticeDate,
		"commit",
		"-qm",
		"Record the unchanged consumer source",
	)
	writeConsumerFile(t, consumer, ".make/upgrade.yml", upgradeFixture(t, "current.yml"))
	start := strings.Index(original, "func Double(")
	later := "package widget\n\n" + strings.Replace(
		original[start:],
		"func Double(",
		"func Later(",
		1,
	)
	writeConsumerFile(t, consumer, "widget/later.go", later)
	arguments := []string{
		"go-mk-notice",
		"GO_MK_BIN=" + engine,
		"GO_MK_NOTICE_MAKEFILE=" + filepath.Join(repository, "go.mk"),
		"GO_MK_PLATFORMS=" + runtime.GOOS + "/" + runtime.GOARCH,
		"GOLANGCI_LINT_FLAGS=-c .make/upgrade.yml",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/upgrade.yml",
		"STATICCHECK_EXTRA_BUILD_REPO=" + filepath.Join(repository, "staticcheck"),
		"STATICCHECK_EXTRA_BUILD_PKG=./cmd/staticcheck-extra",
	}
	if output, err := runConsumerMake(consumer, arguments...); err != nil {
		t.Fatalf("upgrade notice failed: %v\n%s", err, output)
	}

	baseline := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt")
	if !strings.Contains(baseline, "Function 'Double' is too long") ||
		strings.Contains(baseline, "Later") {
		t.Fatalf(
			"upgrade notice did not distinguish committed and uncommitted findings:\n%s",
			baseline,
		)
	}

	arguments[0] = "lint-golangci"
	output, err := runConsumerMake(consumer, arguments...)
	if err == nil || !strings.Contains(output, "Function 'Later' is too long") {
		t.Fatalf("the gate accepted the uncommitted violation: %v\n%s", err, output)
	}
}

func upgradeTokens(t *testing.T, source string) []string {
	t.Helper()
	positions := token.NewFileSet()
	file := positions.AddFile("consumer.go", -1, len(source))
	var lexer scanner.Scanner
	lexer.Init(
		file,
		[]byte(source),
		func(_ token.Position, message string) { t.Error(message) },
		scanner.ScanComments,
	)
	var tokens []string

	for {
		_, kind, literal := lexer.Scan()
		if kind == token.EOF {
			return tokens
		}

		if kind == token.RPAREN && len(tokens) > 0 && tokens[len(tokens)-1] == ",:" {
			tokens = tokens[:len(tokens)-1]
		}

		tokens = append(tokens, kind.String()+":"+literal)
	}
}
