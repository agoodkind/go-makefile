package lint

import (
	"errors"
	"slices"
	"testing"
)

func TestPrepareToolsHonorsInstallOverrides(t *testing.T) {
	t.Setenv("GOLANGCI_LINT", "golangci-lint")
	t.Setenv("GOLANGCI_LINT_INSTALL", "example.com/linter@v1.0.0")
	t.Setenv("GOFUMPT_INSTALL", "example.com/formatter@v2.0.0")
	t.Setenv("GOIMPORTS_INSTALL", "example.com/imports@v3.0.0")

	var installed []string
	err := PrepareTools(
		func(spec string) error {
			installed = append(installed, spec)
			return nil
		},
		func(string) bool {
			t.Fatal("a command name must use the configured installer")
			return false
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"example.com/linter@v1.0.0",
		"example.com/formatter@v2.0.0",
		"example.com/imports@v3.0.0",
	}
	if !slices.Equal(installed, want) {
		t.Fatalf("installed = %v, want %v", installed, want)
	}
}

func TestPrepareToolsReportsInstallFailure(t *testing.T) {
	t.Setenv("GOLANGCI_LINT", "golangci-lint")
	want := errors.New("installation failed")
	err := PrepareTools(
		func(string) error { return want },
		func(string) bool { return false },
	)
	if !errors.Is(err, want) {
		t.Fatalf("installation error = %v, want %v", err, want)
	}
}
