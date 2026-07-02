// Package selfupdate implements release discovery, verification, and installation.
package selfupdate

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout      = 2 * time.Minute
	defaultUpdateInterval   = 24 * time.Hour
	defaultGitHubAPIBaseURL = "https://api.github.com"
	maxExtractedBinaryBytes = 128 * 1024 * 1024
	maxDownloadedAssetBytes = 256 * 1024 * 1024
)

var (
	timeNow                        = time.Now
	updateWithLock                 = WithLock
	updateFetchLatestRelease       = fetchLatestRelease
	updateDownloadFile             = downloadFile
	updateVerifyChecksum           = verifyChecksum
	updateVerifyGitHubAttestations = verifyGitHubAttestations
	updateExtractCandidate         = extractCandidate
	updateValidateCandidate        = validateCandidate
	updateReplaceBinary            = replaceBinary
)

// Config describes the target repository and current binary identity.
type Config struct {
	Repo              string
	Binary            string
	CurrentVersion    string
	CurrentCommit     string
	CurrentBuildHash  string
	AllowPrerelease   *bool
	Interval          time.Duration
	SignerWorkflowURI string
	APIBaseURL        string
	APIBaseURLEnv     string
	AuthToken         string
	ValidateArgs      []string
	ValidateMatch     string
}

// Options configures one update check or apply operation.
type Options struct {
	Config      Config
	Client      *http.Client
	InstallPath string
	CacheDir    string
	StatePath   string
	DryRun      bool
	Log         *slog.Logger
}

// CheckResult describes the current and latest release view.
type CheckResult struct {
	CurrentVersion   string
	CurrentCommit    string
	CurrentBuildHash string
	LatestTag        string
	LatestURL        string
	AssetName        string
	UpdateAvailable  bool
}

// ApplyResult describes one attempted apply operation.
type ApplyResult struct {
	CheckResult
	Applied bool
	DryRun  bool
}

// Check records the latest allowed release and whether an update is available.
func Check(ctx context.Context, options Options) (CheckResult, error) {
	resolvedOptions := resolveOptions(options)
	cfg := resolvedOptions.Config
	if err := cfg.validate(); err != nil {
		return CheckResult{}, err
	}
	latest, err := updateFetchLatestRelease(ctx, resolvedOptions)
	if err != nil {
		recordCheckError(resolvedOptions, err)
		return CheckResult{}, err
	}
	asset, err := selectArchiveAsset(latest.Assets, cfg.Binary)
	if err != nil {
		recordCheckError(resolvedOptions, err)
		return CheckResult{}, err
	}
	result := CheckResult{
		CurrentVersion:   cfg.CurrentVersion,
		CurrentCommit:    cfg.CurrentCommit,
		CurrentBuildHash: cfg.CurrentBuildHash,
		LatestTag:        latest.TagName,
		LatestURL:        latest.HTMLURL,
		AssetName:        asset.Name,
		UpdateAvailable:  releaseIsNewer(cfg.CurrentVersion, latest.TagName),
	}
	var state State
	state.LastCheckAt = timeNow()
	state.LatestTag = result.LatestTag
	state.InstalledVersion = result.CurrentVersion
	state.InstalledCommit = result.CurrentCommit
	state.InstalledBuildHash = result.CurrentBuildHash
	state.LastResult = "check"
	state.NextCheckAt = state.LastCheckAt.Add(cfg.interval())
	if previousState, loadErr := LoadState(resolvedOptions.StatePath); loadErr == nil {
		state.AppliedTag = previousState.AppliedTag
	}
	if err := SaveState(resolvedOptions.StatePath, state); err != nil {
		return result, err
	}
	return result, nil
}

// Apply stages, verifies, and installs the latest allowed release.
func Apply(ctx context.Context, options Options) (ApplyResult, error) {
	resolvedOptions := resolveOptions(options)
	if err := resolvedOptions.Config.validate(); err != nil {
		return ApplyResult{}, err
	}
	var result ApplyResult
	err := updateWithLock(ctx, resolvedOptions.StatePath, func() error {
		check, checkErr := Check(ctx, resolvedOptions)
		if checkErr != nil {
			return checkErr
		}
		result.CheckResult = check
		result.DryRun = resolvedOptions.DryRun
		if !check.UpdateAvailable {
			return saveApplyState(resolvedOptions, result, "current", "")
		}
		return applyLatest(ctx, resolvedOptions, &result)
	})
	if err != nil {
		recordCheckError(resolvedOptions, err)
		return result, err
	}
	return result, nil
}

func applyLatest(ctx context.Context, options Options, result *ApplyResult) error {
	latest, err := updateFetchLatestRelease(ctx, options)
	if err != nil {
		options.Log.WarnContext(ctx, "update apply latest release lookup failed", "err", err)
		return err
	}
	asset, err := selectArchiveAsset(latest.Assets, options.Config.Binary)
	if err != nil {
		options.Log.WarnContext(ctx, "update apply asset selection failed", "tag", latest.TagName, "err", err)
		return err
	}
	cacheDir := options.CacheDir
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		options.Log.WarnContext(ctx, "update apply cache dir create failed", "path", cacheDir, "err", err)
		return fmt.Errorf("create update cache dir: %w", err)
	}
	archivePath := filepath.Join(cacheDir, filepath.Base(asset.Name))
	if err := updateDownloadFile(ctx, options.Client, asset.BrowserDownloadURL, archivePath); err != nil {
		return err
	}
	if err := updateVerifyChecksum(ctx, options, latest, asset, archivePath); err != nil {
		return err
	}
	if err := updateVerifyGitHubAttestations(ctx, options, latest, asset, archivePath); err != nil {
		return err
	}
	candidatePath, cleanup, err := updateExtractCandidate(archivePath, options.Config.Binary)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := updateValidateCandidate(ctx, options.Config, candidatePath); err != nil {
		return err
	}
	if result.DryRun {
		return saveApplyState(options, *result, "dry_run", "")
	}
	if err := updateReplaceBinary(candidatePath, options.InstallPath); err != nil {
		return err
	}
	result.Applied = true
	return saveApplyState(options, *result, "applied", "")
}

func resolveOptions(options Options) Options {
	options.Config = options.Config.withDefaults()
	if options.Client == nil {
		options.Client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if options.CacheDir == "" {
		options.CacheDir = DefaultCacheDir(options.Config.Binary)
	}
	if options.StatePath == "" {
		options.StatePath = DefaultStatePath(options.Config.Binary)
	}
	if options.InstallPath == "" {
		if exe, err := os.Executable(); err == nil {
			options.InstallPath = exe
		}
	}
	if options.Log == nil {
		options.Log = slog.Default()
	}
	return options
}

func saveApplyState(options Options, result ApplyResult, status string, errorMessage string) error {
	var state State
	if previousState, loadErr := LoadState(options.StatePath); loadErr == nil {
		state.AppliedTag = previousState.AppliedTag
	}
	state.LastCheckAt = timeNow()
	state.LatestTag = result.LatestTag
	state.InstalledVersion = result.CurrentVersion
	state.InstalledCommit = result.CurrentCommit
	state.InstalledBuildHash = result.CurrentBuildHash
	state.LastResult = status
	state.LastError = errorMessage
	state.NextCheckAt = state.LastCheckAt.Add(options.Config.interval())
	if result.Applied {
		state.AppliedTag = result.LatestTag
	}
	return SaveState(options.StatePath, state)
}

func recordCheckError(options Options, err error) {
	if err == nil {
		return
	}
	state, loadErr := LoadState(options.StatePath)
	if loadErr != nil {
		var emptyState State
		state = emptyState
	}
	state.LastCheckAt = timeNow()
	state.LastResult = "error"
	state.LastError = err.Error()
	state.NextCheckAt = state.LastCheckAt.Add(options.Config.interval())
	if saveErr := SaveState(options.StatePath, state); saveErr != nil && options.Log != nil {
		options.Log.Warn("save update error state failed", "err", saveErr)
	}
}

func (cfg Config) withDefaults() Config {
	if len(cfg.ValidateArgs) == 0 {
		cfg.ValidateArgs = []string{"version"}
	}
	if cfg.ValidateMatch == "" {
		cfg.ValidateMatch = "version:"
	}
	return cfg
}

func (cfg Config) validate() error {
	if strings.TrimSpace(cfg.Repo) == "" {
		return fmt.Errorf("update repo is required")
	}
	if strings.TrimSpace(cfg.Binary) == "" {
		return fmt.Errorf("update binary is required")
	}
	if strings.TrimSpace(cfg.CurrentVersion) == "" {
		return fmt.Errorf("current version is required")
	}
	if strings.TrimSpace(cfg.CurrentCommit) == "" {
		return fmt.Errorf("current commit is required")
	}
	if strings.TrimSpace(cfg.CurrentBuildHash) == "" {
		return fmt.Errorf("current build hash is required")
	}
	return nil
}

func (cfg Config) allowPrerelease() bool {
	if cfg.AllowPrerelease == nil {
		return true
	}
	return *cfg.AllowPrerelease
}

func (cfg Config) interval() time.Duration {
	if cfg.Interval <= 0 {
		return defaultUpdateInterval
	}
	return cfg.Interval
}

func (cfg Config) signerWorkflowURI() string {
	if cfg.SignerWorkflowURI == "" {
		return goMakefileReleaseBuildWorkflowURI
	}
	return cfg.SignerWorkflowURI
}

func (cfg Config) validateArgs() []string {
	if len(cfg.ValidateArgs) == 0 {
		return []string{"version"}
	}
	return cfg.ValidateArgs
}

func (cfg Config) validateMatch() string {
	if cfg.ValidateMatch == "" {
		return "version:"
	}
	return cfg.ValidateMatch
}

func until(target time.Time) time.Duration {
	return target.Sub(timeNow())
}
