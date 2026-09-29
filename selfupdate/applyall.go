package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// stagedCandidate is a verified and validated release binary written beside
// the installed binary it replaces.
type stagedCandidate struct {
	binary      string
	path        string
	installPath string
	cleanup     func()
}

// ApplyAll installs the latest allowed release for every binary in options as
// one unit. Every option must name the same repository and state path.
//
// Each candidate is written as a hidden file in its install directory and runs
// from there during validation. A binary that loads a shared library from its
// own directory (an @loader_path or $ORIGIN runpath) finds the same library it
// loads after install. ApplyAll validates every candidate before it replaces
// any installed binary. A candidate that fails validation stops the update
// before any rename. A dry run also writes and validates each candidate in the
// install directory, then removes it.
//
// A process killed during an update can leave hidden candidate and backup
// files in the install directory. The next ApplyAll call on the same install
// paths renames each leftover backup over its install path and removes every
// leftover candidate before it checks for a release. A kill during the rename
// step therefore ends with the set of binaries installed before that update.
func ApplyAll(ctx context.Context, options []Options) ([]ApplyResult, error) {
	if len(options) == 0 {
		return nil, fmt.Errorf("update options are required")
	}
	resolvedOptions := make([]Options, 0, len(options))
	for _, option := range options {
		resolved := resolveOptions(option)
		if err := resolved.Config.validate(); err != nil {
			return nil, err
		}
		resolvedOptions = append(resolvedOptions, resolved)
	}
	if err := validateApplySet(resolvedOptions); err != nil {
		return nil, err
	}
	results := make([]ApplyResult, len(resolvedOptions))
	err := updateWithLock(ctx, resolvedOptions[0].StatePath, func() error {
		return applySet(ctx, resolvedOptions, results)
	})
	if err != nil {
		recordCheckError(resolvedOptions[0], err)
		return results, err
	}
	return results, nil
}

func validateApplySet(options []Options) error {
	first := options[0]
	for _, option := range options[1:] {
		if option.Config.Repo != first.Config.Repo {
			return fmt.Errorf("update set mixes repositories %s and %s", first.Config.Repo, option.Config.Repo)
		}
		if option.StatePath != first.StatePath {
			return fmt.Errorf("update set mixes state paths %s and %s", first.StatePath, option.StatePath)
		}
	}
	return nil
}

func applySet(ctx context.Context, options []Options, results []ApplyResult) error {
	if err := recoverInterruptedInstall(options); err != nil {
		return err
	}
	anyUpdateAvailable := false
	for index, option := range options {
		check, err := Check(ctx, option)
		if err != nil {
			return err
		}
		results[index].CheckResult = check
		results[index].DryRun = option.DryRun
		anyUpdateAvailable = anyUpdateAvailable || check.UpdateAvailable
	}
	if !anyUpdateAvailable {
		return saveApplySetState(options, results)
	}

	// Every binary stages from this one release lookup. A release published
	// during the update cannot mix two tags in one install.
	latest, err := updateFetchLatestRelease(ctx, options[0])
	if err != nil {
		options[0].Log.WarnContext(ctx, "update apply latest release lookup failed", "err", err)
		return err
	}
	staged := make([]stagedCandidate, 0, len(options))
	defer func() {
		for _, candidate := range staged {
			candidate.cleanup()
		}
	}()
	toInstall := make([]stagedCandidate, 0, len(options))
	for index, option := range options {
		if !results[index].UpdateAvailable {
			continue
		}
		results[index].LatestTag = latest.TagName
		candidate, err := stageCandidate(ctx, option, latest)
		if err != nil {
			return err
		}
		staged = append(staged, candidate)
		if !option.DryRun {
			toInstall = append(toInstall, candidate)
		}
	}
	if err := installCandidates(toInstall); err != nil {
		options[0].Log.WarnContext(ctx, "update install candidates failed", "err", err)
		return err
	}
	for index, option := range options {
		results[index].Applied = results[index].UpdateAvailable && !option.DryRun
	}
	return saveApplySetState(options, results)
}

// recoverInterruptedInstall runs under the update lock. For each install path
// it renames one leftover backup over the install path, removes any other
// leftover backup, and removes every leftover candidate.
func recoverInterruptedInstall(options []Options) error {
	for _, option := range options {
		if strings.TrimSpace(option.InstallPath) == "" {
			continue
		}
		installDir := filepath.Dir(option.InstallPath)
		installName := filepath.Base(option.InstallPath)
		backups, err := filepath.Glob(filepath.Join(installDir, "."+installName+"-previous-*"))
		if err != nil {
			slog.Warn("update leftover backup lookup failed", "install_path", option.InstallPath, "err", err)
			return fmt.Errorf("find leftover backups for %s: %w", option.InstallPath, err)
		}
		candidates, err := filepath.Glob(filepath.Join(installDir, "."+installName+"-candidate-*"))
		if err != nil {
			slog.Warn("update leftover candidate lookup failed", "install_path", option.InstallPath, "err", err)
			return fmt.Errorf("find leftover candidates for %s: %w", option.InstallPath, err)
		}
		for index, backupPath := range backups {
			if index > 0 {
				slog.Warn("update removed extra leftover backup", "path", backupPath)
				_ = os.Remove(backupPath)
				continue
			}
			if err := os.Rename(backupPath, option.InstallPath); err != nil {
				slog.Warn("update leftover backup restore failed", "path", backupPath, "err", err)
				return fmt.Errorf("restore leftover backup %s: %w", backupPath, err)
			}
			slog.Warn("update restored binary from interrupted install", "install_path", option.InstallPath, "backup", backupPath)
		}
		for _, candidatePath := range candidates {
			slog.Warn("update removed leftover candidate", "path", candidatePath)
			_ = os.Remove(candidatePath)
		}
	}
	return nil
}

func saveApplySetState(options []Options, results []ApplyResult) error {
	for index, option := range options {
		status := "current"
		if results[index].Applied {
			status = "applied"
		} else if results[index].UpdateAvailable && results[index].DryRun {
			status = "dry_run"
		}
		if err := saveApplyState(option, results[index], status, ""); err != nil {
			return err
		}
	}
	return nil
}

// stageCandidate downloads and verifies one release archive, writes its binary
// as a hidden file in the install directory, and validates that file there.
func stageCandidate(ctx context.Context, options Options, latest release) (stagedCandidate, error) {
	if strings.TrimSpace(options.InstallPath) == "" {
		return stagedCandidate{}, fmt.Errorf("install path is empty")
	}
	asset, err := selectArchiveAsset(latest.Assets, options.Config.Binary)
	if err != nil {
		options.Log.WarnContext(ctx, "update apply asset selection failed", "tag", latest.TagName, "err", err)
		return stagedCandidate{}, err
	}
	cacheDir := options.CacheDir
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		options.Log.WarnContext(ctx, "update apply cache dir create failed", "path", cacheDir, "err", err)
		return stagedCandidate{}, fmt.Errorf("create update cache dir: %w", err)
	}
	archivePath := filepath.Join(cacheDir, filepath.Base(asset.Name))
	if err := downloadReleaseAsset(ctx, options, asset, archivePath); err != nil {
		return stagedCandidate{}, err
	}
	if err := updateVerifyChecksum(ctx, options, latest, asset, archivePath); err != nil {
		return stagedCandidate{}, err
	}
	if err := updateVerifyGitHubAttestations(ctx, options, latest, asset, archivePath); err != nil {
		return stagedCandidate{}, err
	}
	candidatePath, cleanup, err := updateExtractCandidate(
		archivePath,
		options.Config.Binary,
		options.Config.MaxBinaryBytes,
		filepath.Dir(options.InstallPath),
	)
	if err != nil {
		return stagedCandidate{}, err
	}
	if err := updateValidateCandidate(ctx, options.Config, candidatePath); err != nil {
		cleanup()
		return stagedCandidate{}, err
	}
	return stagedCandidate{
		binary:      options.Config.Binary,
		path:        candidatePath,
		installPath: options.InstallPath,
		cleanup:     cleanup,
	}, nil
}

// installCandidates renames every staged candidate over its install path. It
// first keeps a hard link or copy of each binary it replaces, and it renames
// those back when a later rename fails.
func installCandidates(candidates []stagedCandidate) error {
	type replacedBinary struct {
		installPath string
		backupPath  string
	}
	slog.Info("update install candidates", "count", len(candidates))
	replaced := make([]replacedBinary, 0, len(candidates))
	restore := func() error {
		var restoreErrors []error
		for index := len(replaced) - 1; index >= 0; index-- {
			entry := replaced[index]
			if entry.backupPath == "" {
				restoreErrors = append(restoreErrors, os.Remove(entry.installPath))
				continue
			}
			restoreErrors = append(restoreErrors, os.Rename(entry.backupPath, entry.installPath))
		}
		return errors.Join(restoreErrors...)
	}
	for _, candidate := range candidates {
		backupPath, err := backupInstalledBinary(candidate.installPath)
		if err != nil {
			joined := errors.Join(err, restore())
			slog.Warn("update install candidates backup failed", "install_path", candidate.installPath, "err", joined)
			return joined
		}
		if err := updateInstallCandidate(candidate.path, candidate.installPath); err != nil {
			if backupPath != "" {
				_ = os.Remove(backupPath)
			}
			joined := errors.Join(err, restore())
			slog.Warn("update install candidates rename failed", "install_path", candidate.installPath, "err", joined)
			return joined
		}
		replaced = append(replaced, replacedBinary{installPath: candidate.installPath, backupPath: backupPath})
	}
	for _, entry := range replaced {
		if entry.backupPath != "" {
			_ = os.Remove(entry.backupPath)
		}
	}
	return nil
}
