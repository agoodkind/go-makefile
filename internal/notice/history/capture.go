package history

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

func (check comparison) inDirectory(
	ctx context.Context,
	directory string,
	rawPath string,
	findingsPath string,
) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return failed("locate notice working directory", err)
	}

	for _, name := range []string{"STATICCHECK_EXTRA_BIN", "GOLANGCI_LINT"} {
		previous, had := os.LookupEnv(name)
		if previous == "" {
			continue
		}

		resolved, err := exec.LookPath(previous)
		if err != nil {
			return failed("resolve notice analyzer executable", err)
		}

		absolute, err := filepath.Abs(resolved)
		if err != nil {
			return failed("resolve absolute notice analyzer path", err)
		}

		if err := os.Setenv(name, absolute); err != nil {
			return failed("set notice analyzer executable", err)
		}

		defer restoreEnvironment(name, previous, had)
	}

	previousRoot, hadRoot := os.LookupEnv("_GO_MK_ROOT")
	if err := os.Setenv("_GO_MK_ROOT", directory); err != nil {
		return failed("set historical notice root", err)
	}

	defer restoreEnvironment("_GO_MK_ROOT", previousRoot, hadRoot)
	if err := os.Chdir(directory); err != nil {
		return failed("enter historical notice directory", err)
	}

	restoration := directoryRestoration{path: workingDir, attempted: false}
	defer restoration.onExit()

	captureErr := check.capturePrepared(ctx, rawPath, findingsPath)
	restoreErr := restoration.restore()
	if err := errors.Join(captureErr, restoreErr); err != nil {
		slog.Error("notice capture historical directory", slog.Any("error", err))

		return err
	}

	return nil
}

func (check comparison) capturePrepared(ctx context.Context, rawPath, findingsPath string) error {
	if err := prepareSource(ctx); err != nil {
		return err
	}

	if err := check.capture(rawPath, findingsPath); err != nil {
		return failed("capture historical notice findings", err)
	}

	return nil
}

func restoreEnvironment(name, previous string, had bool) {
	var err error
	if had {
		err = os.Setenv(name, previous)
	} else {
		err = os.Unsetenv(name)
	}

	if err != nil {
		slog.Error("notice restore environment", slog.String("name", name), slog.Any("error", err))
	}
}
