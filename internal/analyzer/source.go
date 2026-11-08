// Package analyzer resolves and installs the staticcheck-extra executable.
package analyzer

import (
	"context"
	"errors"
	"fmt"
	"go/build"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/mod/module"
)

var (
	errMissingSourceSpec = errors.New("STATICCHECK_EXTRA_INSTALL is required for a source installation")
	errInvalidSourceSpec = errors.New("STATICCHECK_EXTRA_INSTALL must contain one valid package specification")
	errSourceOptions     = errors.New("STATICCHECK_EXTRA_INSTALL cannot contain options or whitespace")
	errInvalidSourcePath = errors.New("STATICCHECK_EXTRA_INSTALL contains an invalid Go import path")
)

// InstallSource builds and links the analyzer from its source specification.
//
// STATICCHECK_EXTRA_INSTALL must contain the Go installation specification.
// The callbacks supply the active Go workspace, executable destination,
// and environment.
//
// The installation uses these callback results:
//   - goPath("GOPATH") selects the Go workspace directory.
//   - outputPath returns the analyzer executable destination.
//   - environment supplies the variables for go install.
func InstallSource(
	goPath func(string) (string, error),
	outputPath func() (string, error),
	environment func() []string,
) error {
	installSpec := os.Getenv("STATICCHECK_EXTRA_INSTALL")
	if installSpec == "" {
		return errMissingSourceSpec
	}

	if err := validateSourceSpec(installSpec); err != nil {
		return err
	}

	binaryName := sourceBinaryName(installSpec)
	gopath, err := goPath("GOPATH")
	if err != nil {
		return err
	}

	goBin := filepath.Join(gopath, "bin")
	installedPath := filepath.Join(goBin, binaryName)
	output, err := outputPath()
	if err != nil {
		return err
	}

	slog.Info("staticcheck install binary", slog.String("spec", installSpec))
	// #nosec G702 -- validateSourceSpec rejects options and extra arguments.
	command := exec.CommandContext(context.Background(), "go", "install", installSpec)
	variables := environment()
	variables = setEnvironment(variables, "GONOSUMDB", "goodkind.io/go-makefile,goodkind.io/go-makefile/staticcheck")
	variables = setEnvironment(variables, "GOBIN", goBin)
	command.Env = variables
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return failure("install analyzer from source", err)
	}

	return linkExecutable(installedPath, output)
}

func linkExecutable(installedPath, output string) error {
	slog.Info("staticcheck link executable", slog.String("source", installedPath), slog.String("output", output))
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return failure("create analyzer executable directory", err)
	}

	if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
		return failure("replace analyzer executable", err)
	}

	if err := os.Symlink(installedPath, output); err != nil {
		return failure("link analyzer executable", err)
	}

	return nil
}

func setEnvironment(environment []string, name, value string) []string {
	prefix := name + "="

	for index, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			environment[index] = prefix + value

			return environment
		}
	}

	return append(environment, prefix+value)
}

func sourceBinaryName(specification string) string {
	packagePath, _, _ := strings.Cut(specification, "@")

	return filepath.Base(packagePath)
}

func validateSourceSpec(specification string) error {
	if strings.HasPrefix(specification, "-") || strings.IndexFunc(specification, unicode.IsSpace) >= 0 {
		return errSourceOptions
	}

	packagePath, query, versioned := strings.Cut(specification, "@")
	if packagePath == "" || (versioned && (query == "" || strings.Contains(query, "@"))) {
		return errInvalidSourceSpec
	}

	if build.IsLocalImport(packagePath) {
		if versioned {
			return errInvalidSourceSpec
		}

		return nil
	}

	if err := module.CheckImportPath(packagePath); err != nil {
		return errInvalidSourcePath
	}

	return nil
}

func failure(message string, err error) error {
	wrapped := fmt.Errorf("%s: %w", message, err)
	slog.Error("staticcheck operation failed", slog.String("operation", message), slog.Any("err", wrapped))

	return wrapped
}
