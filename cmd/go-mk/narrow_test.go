package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
	"goodkind.io/go-makefile/internal/baseline"
)

const selectedLengthScope = `^widget/(selected|later)[.]go:.*file-length-limit.*\(revive\)$`

func TestAutomaticGolangciScopeKeepsOnlySelectedHistoricalFindings(t *testing.T) {
	consumer := newConsumerRepository(t, beforeNoticeDate)

	for _, source := range []struct{ file, function string }{
		{"selected", "Selected"},
		{"unselected", "Unselected"},
	} {
		writeConsumerFile(
			t,
			consumer,
			"widget/"+source.file+".go",
			"package widget\n"+strings.Repeat("// Historical source.\n", 1001)+
				"func "+source.function+"(value int) int {\nreturn value * 42\n}\n",
		)
	}

	writeConsumerFile(
		t,
		consumer,
		"widget/previous.go",
		"package widget\nfunc Previous(value int) int { return value * 59 }\n",
	)
	runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(t, consumer, beforeNoticeDate, "commit", "-qm", "Add historical diagnostics")
	writeConsumerFile(
		t,
		consumer,
		"widget/later.go",
		"package widget\n"+strings.Repeat("// Later source.\n", 1001)+
			"func Later(value int) int { return value * 43 }\n",
	)
	runConsumerGit(t, consumer, afterNoticeDate, "add", ".")
	runConsumerGit(t, consumer, afterNoticeDate, "commit", "-qm", "Add later selected diagnostic")
	configuration, err := os.ReadFile(filepath.Join("testdata", "scope.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/golangci.yml", string(configuration))
	output, gateErr := runConsumerMake(
		consumer,
		"lint-golangci-scope",
		"GOLANGCI_LINT_FLAGS=-c .make/golangci.yml",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml",
		"GOLANGCI_LINT_TARGETS=./widget",
		"GOLANGCI_LINT_BASELINE_SCOPE_PATTERN=.*",
	)
	if gateErr == nil {
		t.Fatalf("the real fixture gate did not report findings:\n%s", output)
	}

	report, readErr := os.ReadFile(filepath.Join(consumer, ".make/golangci-lint-scope.out"))
	if readErr != nil {
		t.Fatalf(
			"the real fixture capture failed: %v; gate error: %v\n%s",
			readErr,
			gateErr,
			output,
		)
	}

	findings := string(report)
	selected := fixtureFinding(t, findings, "widget/selected.go:", "file-length-limit", output)
	fixtureFinding(t, findings, "widget/unselected.go:", "file-length-limit", output)
	fixtureFinding(t, findings, "widget/selected.go:", "(mnd)", output)
	previous := fixtureFinding(t, findings, "widget/previous.go:", "(mnd)", output)
	oldRow := previous + "\t# golangci-lint:first_added=" + beforeNoticeDate + " last_seen=" + beforeNoticeDate
	writeConsumerFile(
		t,
		consumer,
		".golangci-lint-baseline.txt",
		"# Previous baseline.\n"+oldRow+"\n",
	)
	engine := gomk.BuiltTestEngine(t)
	output = executeScopedNotice(t, engine, consumer, "201", selectedLengthScope)
	if strings.Contains(output, "auto-baseline failed") ||
		!strings.Contains(output, "1 added, 1 remaining") {
		t.Fatalf("the selected historical capture did not report one addition:\n%s", output)
	}

	written := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt")
	keys := make(map[string]bool)

	for _, row := range strings.Split(written, "\n") {
		if row == "" || strings.HasPrefix(row, "#") {
			continue
		}

		finding, _, _ := strings.Cut(row, "\t# ")
		keys[baseline.Key(finding)] = true
	}

	if len(keys) != 2 || !keys[baseline.Key(previous)] || !keys[baseline.Key(selected)] {
		t.Fatalf("scoped capture admitted unselected keys:\n%s", written)
	}

	if !strings.Contains(written, "\n"+oldRow+"\n") {
		t.Fatalf("the existing out-of-scope row changed:\n%s", written)
	}

	output, gateErr = runConsumerMake(
		consumer,
		"lint-golangci-scope",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml",
		"GOLANGCI_LINT_TARGETS=./widget",
		"GOLANGCI_LINT_BASELINE_SCOPE_PATTERN="+selectedLengthScope,
	)
	if gateErr == nil || !strings.Contains(output, "widget/later.go") ||
		strings.Contains(output, "widget/selected.go") {
		t.Fatalf(
			"the later selected finding did not remain a gate failure: %v\n%s",
			gateErr,
			output,
		)
	}

	output = executeScopedNotice(t, engine, consumer, "202", "[")
	if !strings.Contains(output, "auto-baseline failed") ||
		!strings.Contains(output, "error parsing regexp:") {
		t.Fatalf("the invalid scope did not fail automatic capture:\n%s", output)
	}

	if actual := readConsumerFile(t, consumer, ".golangci-lint-baseline.txt"); actual != written {
		t.Fatalf("the invalid scope changed the baseline:\n%s", actual)
	}

	if applied := readConsumerFile(t, consumer, appliedNoticeFile); strings.Contains(
		applied,
		"202\n",
	) {
		t.Fatalf("the invalid scope recorded an acknowledgment: %q", applied)
	}

	if captured := readConsumerFile(
		t,
		consumer,
		".make/golangci-lint-scope-baseline.out",
	); captured != "" {
		t.Fatalf("the invalid scope broadened captured findings:\n%s", captured)
	}
}

func fixtureFinding(t *testing.T, findings, prefix, rule, output string) string {
	t.Helper()

	for _, line := range strings.Split(findings, "\n") {
		if strings.HasPrefix(line, prefix) && strings.Contains(line, rule) {
			return line
		}
	}

	t.Fatalf(
		"the real fixture did not emit %s %s:\n%s\nTool output:\n%s",
		prefix,
		rule,
		findings,
		output,
	)

	return ""
}

func executeScopedNotice(t *testing.T, engine, consumer, id, pattern string) string {
	t.Helper()
	writeConsumerFile(
		t,
		consumer,
		"scoped-notice.txt",
		id+
			"\tINTRODUCED=2026-05-25T19:38:46-07:00 GATE=golangci RULE=file-length-limit PATTERN="+
			pattern+"\tCapture selected historical findings\n",
	)
	command := exec.CommandContext(t.Context(), engine, "notice")
	command.Dir = consumer
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"_GO_MK_ROOT=" + consumer,
		"_GO_MK_NOTICES_FILE=" + filepath.Join(consumer, "scoped-notice.txt"),
		"GOLANGCI_LINT_FLAGS=-c .make/golangci.yml",
		"GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml",
		"GOLANGCI_LINT_TARGETS=./widget",
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("the public notice command failed: %v\n%s", err, output)
	}

	return string(output)
}
