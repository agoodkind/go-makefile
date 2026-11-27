package lint

import (
	"fmt"
	"os"
	"path/filepath"
)

func PrepareTools(install func(string) error, executable func(string) bool) error {
	binary := os.Getenv("GOLANGCI_LINT")
	if filepath.IsAbs(binary) {
		if !executable(binary) {
			return fmt.Errorf("GOLANGCI_LINT is not executable: %s", binary)
		}

		return nil
	}

	tools := [...]struct {
		variable string
		spec     string
	}{
		{"GOLANGCI_LINT_INSTALL", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0"},
		{"GOFUMPT_INSTALL", "mvdan.cc/gofumpt@v0.10.0"},
		{"GOIMPORTS_INSTALL", "golang.org/x/tools/cmd/goimports@v0.45.0"},
	}

	for _, tool := range tools {
		spec := os.Getenv(tool.variable)
		if spec == "" {
			spec = tool.spec
		}

		if err := install(spec); err != nil {
			return err
		}
	}

	return nil
}
