package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMakeFmtAppliesArgumentAndWhitespaceRules(t *testing.T) {
	t.Parallel()
	consumer := newConsumerRepository(t, afterNoticeDate)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	settings, err := os.ReadFile(filepath.Join(repository, "golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/format.yml", string(settings))
	writeConsumerFile(t, consumer, "widget/widget.go", `package widget

func sum(first, second int) int { return first + second }

func Double(value int) int {
	if value < 0 {
		return 0
	}
	return sum(
		value, value,
	)
}
`)
	configuration := "GOLANGCI_LINT_FLAGS=-c .make/format.yml"
	flags := "STATICCHECK_EXTRA_FLAGS=-argumentlines"
	output, err := runConsumerMake(consumer, "staticcheck-extra", flags, "STATICCHECK_EXTRA_TEST_FLAGS=")
	if err == nil || !strings.Contains(output, "argumentlines:") {
		t.Fatalf("argument gate did not reject shared argument lines: %v\n%s", err, output)
	}

	output, err = runConsumerMake(consumer, "lint-golangci", "GOLANGCI_LINT_RUN_FLAGS=-c .make/format.yml --enable-only=wsl_v5")
	if err == nil || !strings.Contains(output, "wsl_v5") {
		t.Fatalf("whitespace gate did not reject missing spacing: %v\n%s", err, output)
	}

	output, err = runConsumerMake(consumer, "fmt", configuration)
	if err != nil {
		t.Fatalf("make fmt failed: %v\n%s", err, output)
	}

	formatted := readConsumerFile(t, consumer, "widget/widget.go")
	if !strings.Contains(formatted, "}\n\n\treturn sum(") || !strings.Contains(formatted, "\t\tvalue,\n\t\tvalue,\n") {
		t.Fatalf("make fmt did not apply both rules:\n%s", formatted)
	}

	output, err = runConsumerMake(consumer, "fmt", configuration)
	if err != nil {
		t.Fatalf("second make fmt failed: %v\n%s", err, output)
	}

	if repeated := readConsumerFile(t, consumer, "widget/widget.go"); repeated != formatted {
		t.Fatalf("the second make fmt changed its output:\n%s", repeated)
	}

	for _, arguments := range [][]string{
		{"staticcheck-extra", flags, "STATICCHECK_EXTRA_TEST_FLAGS="},
		{"lint-golangci", "GOLANGCI_LINT_RUN_FLAGS=-c .make/format.yml --enable-only=wsl_v5"},
	} {
		if output, err := runConsumerMake(consumer, arguments...); err != nil {
			t.Fatalf("formatted consumer failed %v: %v\n%s", arguments, err, output)
		}
	}

	command := exec.Command("go", "test", "./widget")
	command.Dir = consumer
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("formatted consumer changed behavior: %v\n%s", err, output)
	}
}

func TestMakeFmtRestrictsWhitespaceToSelectedFiles(t *testing.T) {
	consumer := newConsumerRepository(t, afterNoticeDate)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	settings, err := os.ReadFile(filepath.Join(repository, "golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/format.yml", string(settings))
	platform := "windows"
	if runtime.GOOS == platform {
		platform = "linux"
	}

	files := map[string]string{
		"widget/selected.go": `package widget

import "errors"

func Selected(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func SelectedError() error {
	err := errors.New("selected")

	if err != nil {
		return err
	}
	return nil
}
`,
		"widget/selected_test.go": `package widget_test

import "testing"

func TestSelected(t *testing.T) {
	if t.Name() == "" {
		t.Fatal("missing name")
	}
	t.Log("selected")
}
`,
		"widget/selected_" + platform + ".go": `package widget

func Platform(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
`,
		"widget/tagged.go": `//go:build formatting

package widget

func Tagged(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
`,
	}
	sibling := `package widget

func Sibling(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
`
	writeConsumerFile(t, consumer, "widget/sibling.go", sibling)
	selected := []string{"widget/selected.go", "widget/selected_test.go", "widget/selected_" + platform + ".go", "widget/tagged.go"}

	for name, contents := range files {
		writeConsumerFile(t, consumer, name, contents)
	}

	if output, err := runConsumerMake(consumer, "fmt", "GOLANGCI_LINT_FLAGS=-c .make/format.yml", "GOLANGCI_FMT_FILES=widget/tagged.go"); err == nil || !strings.Contains(output, "set run.build-tags") {
		t.Fatalf("make fmt did not reject a disabled selected source: %v\n%s", err, output)
	}

	writeConsumerFile(t, consumer, ".make/format.yml", strings.Replace(string(settings), "  timeout: 10m", "  timeout: 10m\n  build-tags: [formatting]", 1))
	arguments := []string{"fmt", "GOLANGCI_LINT_FLAGS=-c .make/format.yml", "GOLANGCI_FMT_FILES=" + strings.Join(selected, " ")}
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
