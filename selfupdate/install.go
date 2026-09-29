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
	if err := downloadReleaseAsset(ctx, options, asset, archivePath); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	if err := updateVerifyChecksum(ctx, options, latest, asset, archivePath); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	if err := updateVerifyGitHubAttestations(ctx, options, latest, asset, archivePath); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	candidatePath, cleanup, err := updateExtractCandidate(
		archivePath,
		options.Config.Binary,
		options.Config.MaxBinaryBytes,
		installOptions.BinDir,
	)
	if err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	defer cleanup()
	installPath := filepath.Join(installOptions.BinDir, options.Config.Binary)
	if err := updateInstallCandidate(candidatePath, installPath); err != nil {
		return InstallReleaseBinaryResult{}, err
	}
	return InstallReleaseBinaryResult{
		Tag:         latest.TagName,
		AssetName:   asset.Name,
		InstallPath: installPath,
	}, nil
}

// InstallReleaseBinariesOptions configures an install of several binaries from
// one release of one repository into one bin directory.
type InstallReleaseBinariesOptions struct {
	Options []Options
	Version string
	Channel ReleaseChannel
	BinDir  string
}

// InstallReleaseBinaries installs every binary in installOptions.Options from
// one release as one unit. It resolves the release once, writes each
// candidate as a hidden file in BinDir, and validates each candidate there
// before it replaces any installed binary. A failed download, verification,
// or validation leaves every installed binary unchanged. The renames use the
// backup, rollback, and commit marker of ApplyAll, and the marker sits beside
// the first option's state file.
func InstallReleaseBinaries(
	ctx context.Context,
	installOptions InstallReleaseBinariesOptions,
) ([]InstallReleaseBinaryResult, error) {
	if len(installOptions.Options) == 0 {
		return nil, fmt.Errorf("install options are required")
	}
	resolvedOptions := make([]Options, 0, len(installOptions.Options))
	for _, option := range installOptions.Options {
		option.InstallPath = filepath.Join(installOptions.BinDir, option.Config.Binary)
		option.DryRun = false
		resolved := resolveOptions(option)
		if err := validateInstallReleaseBinaryInput(resolved.Config, installOptions.BinDir); err != nil {
			return nil, err
		}
		resolvedOptions = append(resolvedOptions, resolved)
	}
	binaries := make(map[string]bool, len(resolvedOptions))
	for _, option := range resolvedOptions {
		if option.Config.Repo != resolvedOptions[0].Config.Repo {
			return nil, fmt.Errorf("install set mixes repositories %s and %s", resolvedOptions[0].Config.Repo, option.Config.Repo)
		}
		if binaries[option.Config.Binary] {
			return nil, fmt.Errorf("install set repeats binary %s", option.Config.Binary)
		}
		binaries[option.Config.Binary] = true
	}
	var results []InstallReleaseBinaryResult
	err := updateWithLock(ctx, resolvedOptions[0].StatePath, func() error {
		installed, installErr := installReleaseSet(ctx, resolvedOptions, installOptions)
		results = installed
		return installErr
	})
	if err != nil {
		resolvedOptions[0].Log.WarnContext(ctx, "release install set failed", "err", err)
		return nil, err
	}
	return results, nil
}

func installReleaseSet(
	ctx context.Context,
	options []Options,
	installOptions InstallReleaseBinariesOptions,
) ([]InstallReleaseBinaryResult, error) {
	if err := recoverInterruptedInstall(options); err != nil {
		return nil, err
	}
	latest, err := resolveRequestedRelease(ctx, options[0], installOptions.Version, installOptions.Channel)
	if err != nil {
		return nil, err
	}
	staged := make([]stagedCandidate, 0, len(options))
	defer func() {
		for _, candidate := range staged {
			candidate.cleanup()
		}
	}()
	results := make([]InstallReleaseBinaryResult, 0, len(options))
	for _, option := range options {
		candidate, err := stageCandidate(ctx, option, latest)
		if err != nil {
			return nil, err
		}
		staged = append(staged, candidate)
		asset, err := selectArchiveAsset(latest.Assets, option.Config.Binary)
		if err != nil {
			return nil, err
		}
		results = append(results, InstallReleaseBinaryResult{
			Tag:         latest.TagName,
			AssetName:   asset.Name,
			InstallPath: option.InstallPath,
		})
	}
	if err := installCandidates(staged, installCommitMarkerPath(options[0])); err != nil {
		return nil, err
	}
	return results, nil
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
