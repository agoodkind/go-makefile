package selfupdate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReleaseChannel selects which GitHub release stream resolves when no exact
// version tag is supplied.
type ReleaseChannel string

const (
	// ReleaseChannelRolling selects the newest non-draft release, including
	// prereleases.
	ReleaseChannelRolling ReleaseChannel = "rolling"
	// ReleaseChannelStable selects GitHub's latest non-prerelease release.
	ReleaseChannelStable ReleaseChannel = "stable"
)

// InstallReleaseBinaryOptions configures one release binary install.
type InstallReleaseBinaryOptions struct {
	Options Options
	Version string
	Channel ReleaseChannel
	BinDir  string
}

// InstallReleaseBinaryResult describes the release asset that was installed.
type InstallReleaseBinaryResult struct {
	Tag         string
	AssetName   string
	InstallPath string
}

// InstallReleaseBinary installs the runtime platform archive for a release.
func InstallReleaseBinary(ctx context.Context, installOptions InstallReleaseBinaryOptions) (InstallReleaseBinaryResult, error) {
	options := resolveOptions(installOptions.Options)
	if err := validateInstallReleaseBinaryInput(options.Config, installOptions.BinDir); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	latest, err := resolveRequestedRelease(ctx, options, installOptions.Version, installOptions.Channel)
	if err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	asset, err := selectArchiveAsset(latest.Assets, options.Config.Binary)
	if err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	if err := os.MkdirAll(options.CacheDir, 0o700); err != nil {
		options.Log.WarnContext(ctx, "release install cache dir create failed", "path", options.CacheDir, "err", err)
		return InstallReleaseBinaryResult{}, fmt.Errorf("create release install cache dir: %w", err)
	}
	archivePath := filepath.Join(options.CacheDir, filepath.Base(asset.Name))
	if err := updateDownloadFile(ctx, options.Client, asset.BrowserDownloadURL, archivePath); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	if err := updateVerifyChecksum(ctx, options, latest, asset, archivePath); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	if err := updateVerifyGitHubAttestations(ctx, options, latest, asset, archivePath); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	candidatePath, cleanup, err := updateExtractCandidate(archivePath, options.Config.Binary)
	if err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	defer cleanup()
	installPath := filepath.Join(installOptions.BinDir, options.Config.Binary)
	if err := os.MkdirAll(installOptions.BinDir, 0o755); err != nil {
		return InstallReleaseBinaryResult{}, fmt.Errorf("create install bin dir: %w", err)
	}
	if err := updateReplaceBinary(candidatePath, installPath); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	return InstallReleaseBinaryResult{
		Tag:         latest.TagName,
		AssetName:   asset.Name,
		InstallPath: installPath,
	}, nil
}

func resolveRequestedRelease(ctx context.Context, options Options, version string, channel ReleaseChannel) (release, error) {
	version = strings.TrimSpace(version)
	if version != "" {
		return fetchReleaseByTag(ctx, options, version)
	}
	resolvedOptions, err := optionsForReleaseChannel(options, channel)
	if err != nil {
		return release{}, err
	}
	return updateFetchLatestRelease(ctx, resolvedOptions)
}

func optionsForReleaseChannel(options Options, channel ReleaseChannel) (Options, error) {
	resolvedChannel, err := normalizeReleaseChannel(channel)
	if err != nil {
		return Options{}, err
	}
	allowPrerelease := resolvedChannel == ReleaseChannelRolling
	options.Config.AllowPrerelease = &allowPrerelease
	return options, nil
}

func normalizeReleaseChannel(channel ReleaseChannel) (ReleaseChannel, error) {
	if channel == "" {
		return ReleaseChannelRolling, nil
	}
	switch channel {
	case ReleaseChannelRolling, ReleaseChannelStable:
		return channel, nil
	default:
		return "", fmt.Errorf("release channel must be rolling or stable")
	}
}

func validateInstallReleaseBinaryInput(cfg Config, binDir string) error {
	if strings.TrimSpace(cfg.Repo) == "" {
		return fmt.Errorf("update repo is required")
	}
	if strings.TrimSpace(cfg.Binary) == "" {
		return fmt.Errorf("update binary is required")
	}
	if strings.TrimSpace(binDir) == "" {
		return fmt.Errorf("install bin dir is required")
	}
	return nil
}
