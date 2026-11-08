package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMakeFmtAppliesArgumentAndWhitespaceRules checks how make fmt
// repairs two failing gates.
func TestMakeFmtAppliesArgumentAndWhitespaceRules(t *testing.T) {
	t.Parallel()
	consumer := newConsumerRepository(t, afterNoticeDate)
	setPendingNotices(t, consumer)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	settings, err := os.ReadFile(filepath.Join(repository, "golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/format.yml", string(settings))
	writeConsumerFile(t, consumer, fixtureWidget, formatSource(t, "widget.go"))
	configuration := "GOLANGCI_LINT_FLAGS=-c .make/format.yml"
	const lintSettings = "GOLANGCI_LINT_RUN_FLAGS=-c .make/format.yml --enable-only=wsl_v5"
	flags := fixtureArgumentFlags
	output, err := runConsumerMake(consumer, fixtureStaticcheckGate, flags, fixtureTestFlagsOff)
	if err == nil || !strings.Contains(output, "argumentlines:") {
		t.Fatalf("argument gate did not reject shared argument lines: %v\n%s", err, output)
	}

	output, err = runConsumerMake(consumer, fixtureGolangciGate, lintSettings)
	if err == nil || !strings.Contains(output, "wsl_v5") {
		t.Fatalf("whitespace gate did not reject missing spacing: %v\n%s", err, output)
	}

	output, err = runConsumerMake(consumer, "fmt", configuration)
	if err != nil {
		t.Fatalf("make fmt failed: %v\n%s", err, output)
	}

	formatted := readConsumerFile(t, consumer, fixtureWidget)
	argumentLines := strings.Repeat("\t\tvalue,\n", 2)
	if !strings.Contains(formatted, "}\n\n\treturn sum(") || !strings.Contains(formatted, argumentLines) {
		t.Fatalf("make fmt did not apply both rules:\n%s", formatted)
	}

	output, err = runConsumerMake(consumer, "fmt", configuration)
	if err != nil {
		t.Fatalf("second make fmt failed: %v\n%s", err, output)
	}

	if repeated := readConsumerFile(t, consumer, fixtureWidget); repeated != formatted {
		t.Fatalf("the second make fmt changed its output:\n%s", repeated)
	}

	for _, arguments := range [][]string{
		{fixtureStaticcheckGate, flags, fixtureTestFlagsOff},
		{fixtureGolangciGate, lintSettings},
	} {
		if output, err := runConsumerMake(consumer, arguments...); err != nil {
			t.Fatalf("formatted consumer failed %v: %v\n%s", arguments, err, output)
		}
	}

	command := exec.CommandContext(t.Context(), "go", "test", "./widget")
	command.Dir = consumer
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("formatted consumer changed behavior: %v\n%s", err, output)
	}
}

// TestMakeFmtRestrictsWhitespaceToSelectedFiles checks that neighboring
// files retain their formatting.
func TestMakeFmtRestrictsWhitespaceToSelectedFiles(t *testing.T) {
	consumer := newConsumerRepository(t, afterNoticeDate)
	setPendingNotices(t, consumer)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	settings, err := os.ReadFile(filepath.Join(repository, "golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/format.yml", string(settings))
	platform := fixtureWindows
	if runtime.GOOS == platform {
		platform = fixtureLinux
	}

	files := map[string]string{
		"widget/selected.go":                  formatSource(t, "selected.go"),
		"widget/selected_test.go":             formatSource(t, "selected_test.go"),
		"widget/selected_" + platform + ".go": formatSource(t, "platform.go"),
		"widget/tagged.go":                    formatSource(t, "tagged.go"),
	}
	sibling := formatSource(t, "sibling.go")
	writeConsumerFile(t, consumer, "widget/sibling.go", sibling)
	selected := []string{
		"widget/selected.go", "widget/selected_test.go",
		"widget/selected_" + platform + ".go", "widget/tagged.go",
	}

	for name, contents := range files {
		writeConsumerFile(t, consumer, name, contents)
	}

	const formatFlags = "GOLANGCI_LINT_FLAGS=-c .make/format.yml"
	output, err := runConsumerMake(consumer, "fmt", formatFlags, "GOLANGCI_FMT_FILES=widget/tagged.go")
	if err == nil || !strings.Contains(output, "set run.build-tags") {
		t.Fatalf("make fmt did not reject a disabled selected source: %v\n%s", err, output)
	}

	enabled := strings.Replace(string(settings), "  timeout: 10m", "  timeout: 10m\n  build-tags: [formatting]", 1)
	writeConsumerFile(t, consumer, ".make/format.yml", enabled)
	arguments := []string{"fmt", formatFlags, "GOLANGCI_FMT_FILES=" + strings.Join(selected, " ")}
	if output, err := runConsumerMake(consumer, arguments...); err != nil {
		t.Fatalf("selected make fmt failed: %v\n%s", err, output)
	}

	formatted := make(map[string]string)

	for _, name := range selected {
		contents := readConsumerFile(t, consumer, name)
		if !strings.Contains(contents, "}\n\n\t") {
			t.Fatalf("make fmt did not fix selected file %s:\n%s", name, contents)
		}

		formatted[name] = contents
	}

	if !strings.Contains(formatted["widget/selected.go"], "err := errors.New(\"selected\")\n\tif err != nil") {
		t.Fatalf("make fmt did not apply the typed err rule:\n%s", formatted["widget/selected.go"])
	}

	if contents := readConsumerFile(t, consumer, "widget/sibling.go"); contents != sibling {
		t.Fatalf("make fmt changed the unselected sibling:\n%s", contents)
	}

	if output, err := runConsumerMake(consumer, arguments...); err != nil {
		t.Fatalf("repeated selected make fmt failed: %v\n%s", err, output)
	}

	for name, expected := range formatted {
		if contents := readConsumerFile(t, consumer, name); contents != expected {
			t.Fatalf("repeated make fmt changed %s:\n%s", name, contents)
		}
	}

	if contents := readConsumerFile(t, consumer, "widget/sibling.go"); contents != sibling {
		t.Fatalf("repeated make fmt changed the unselected sibling:\n%s", contents)
	}
}

func formatSource(t *testing.T, name string) string {
	t.Helper()

	return readConsumerFile(t, filepath.Join("testdata", "format"), name)
}
