package analyzer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"goodkind.io/go-makefile/selfupdate"
)

// InstallRelease installs the verified analyzer from the rolling release.
//
// The downloaded binary must advertise the flags that the caller requires.
// Release verification completes before the installer replaces the executable.
//
// The caller supplies these inputs:
//   - outputPath returns the executable destination.
//   - flags returns the required analyzer flags.
//   - repository selects the GitHub release repository.
func InstallRelease(
	outputPath func() (string, error),
	flags func() string,
	repository string,
) error {
	slog.Info("staticcheck install rolling release")
	output, err := outputPath()
	if err != nil {
		return err
	}

	directory := filepath.Dir(output)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return failure("create analyzer release directory", err)
	}

	cache := filepath.Join(directory, "staticcheck-release")
	installed, err := selfupdate.InstallReleaseBinaries(context.Background(), selfupdate.InstallReleaseBinariesOptions{
		Channel: selfupdate.ReleaseChannelRolling,
		BinDir:  directory,
		Options: []selfupdate.Options{{
			Config: selfupdate.Config{
				Repo:          repository,
				Binary:        "staticcheck-extra",
				WebBaseURLEnv: "STATICCHECK_EXTRA_RELEASE_BASE_URL",
				ValidateArgs:  append(strings.Fields(flags()), "-flags"),
				ValidateMatch: "\"Name\"",
			},
			CacheDir:  cache,
			StatePath: filepath.Join(cache, "state.json"),
		}},
	})
	if err != nil {
		return failure("install verified analyzer release", err)
	}

	slog.Info("staticcheck installed release", slog.String("tag", installed[0].Tag))

	return nil
}
