package analyzer

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Resolve selects and prepares the analyzer executable.
//
// The resolver uses the first configured source and checks the requested
// analyzer flags before reusing an executable.
//
// The selection order is:
//   - The explicit STATICCHECK_EXTRA_BIN executable.
//   - The STATICCHECK_EXTRA_BUILD_REPO local checkout.
//   - The explicit STATICCHECK_EXTRA_INSTALL source specification.
//   - The verified rolling release when a source specification is absent.
func Resolve(
	outputPath func() (string, error),
	executable func(string) bool,
	missingFlags func(string) bool,
	newerSource func(string, string) (bool, error),
	build func() error,
	installSource func() error,
	installRelease func() error,
	goPath func(string) (string, error),
	write func(string),
	invalidBinary error,
) error {
	slog.Info("staticcheck resolve binary")
	configured := os.Getenv("STATICCHECK_EXTRA_BIN")
	repository := os.Getenv("STATICCHECK_EXTRA_BUILD_REPO")
	installSpec := os.Getenv("STATICCHECK_EXTRA_INSTALL")
	output, err := outputPath()
	if err != nil {
		return err
	}

	if configured != "" {
		return resolveConfigured(configured, executable, missingFlags, write, invalidBinary)
	}

	if repository != "" {
		return resolveCheckout(repository, output, executable, missingFlags, newerSource, build, write, invalidBinary)
	}

	if installSpec == "" {
		return installRelease()
	}

	if strings.HasSuffix(installSpec, "@latest") {
		return installSource()
	}

	return resolveInstalled(installSpec, output, goPath, executable, missingFlags, installSource)
}

func resolveConfigured(
	configured string,
	executable func(string) bool,
	missingFlags func(string) bool,
	write func(string),
	invalidBinary error,
) error {
	if !executable(configured) {
		write("staticcheck-extra: " + configured + " not executable\n")

		return invalidBinary
	}

	if missingFlags(configured) {
		write("staticcheck-extra: " + configured + " does not support requested flags\n")

		return invalidBinary
	}

	return nil
}

func resolveCheckout(
	repository string,
	output string,
	executable func(string) bool,
	missingFlags func(string) bool,
	newerSource func(string, string) (bool, error),
	build func() error,
	write func(string),
	invalidBinary error,
) error {
	checkout, err := os.OpenRoot(repository)
	if err != nil {
		return failure("open analyzer build repository", err)
	}

	defer func() { _ = checkout.Close() }()

	if os.Getenv("STATICCHECK_EXTRA_BUILD_PKG") == "" {
		write("staticcheck-extra: STATICCHECK_EXTRA_BUILD_PKG not set\n")

		return invalidBinary
	}

	stale := !executable(output)
	if !stale {
		newer, err := newerSource(repository, output)
		if err != nil {
			return err
		}

		stale = newer
	}

	if !stale {
		stale = missingFlags(output)
	}

	if stale {
		return build()
	}

	return nil
}

func resolveInstalled(
	installSpec string,
	output string,
	goPath func(string) (string, error),
	executable func(string) bool,
	missingFlags func(string) bool,
	installSource func() error,
) error {
	binaryName := sourceBinaryName(installSpec)
	gopath, err := goPath("GOPATH")
	if err != nil {
		return err
	}

	installedPath := filepath.Join(gopath, "bin", binaryName)
	if !executable(installedPath) || missingFlags(installedPath) {
		return installSource()
	}

	return linkExecutable(installedPath, output)
}
