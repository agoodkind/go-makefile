package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/semver"
)

type release struct {
	HTMLURL    string         `json:"html_url"`
	TagName    string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

func fetchLatestRelease(ctx context.Context, options Options) (release, error) {
	log := options.Log
	repo := options.Config.Repo
	if options.Config.allowPrerelease() {
		releases, err := fetchReleaseList(ctx, options, repo)
		if err != nil {
			log.WarnContext(ctx, "update release list query failed", "repo", repo, "err", err)
			return release{}, err
		}
		for _, candidate := range releases {
			if !candidate.Draft {
				return candidate, nil
			}
		}
		noReleaseErr := fmt.Errorf("no non-draft releases found for %s", repo)
		log.WarnContext(ctx, "update release list had no eligible release", "repo", repo, "err", noReleaseErr)
		return release{}, noReleaseErr
	}
	url := releaseAPIBaseURL(options.Config) + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.WarnContext(ctx, "update latest release request build failed", "repo", repo, "err", err)
		return release{}, fmt.Errorf("build latest release request: %w", err)
	}
	applyGitHubAPIHeaders(req, options.Config)
	resp, err := options.Client.Do(req)
	if err != nil {
		log.WarnContext(ctx, "update latest release request failed", "repo", repo, "err", err)
		return release{}, fmt.Errorf("query latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("query latest release: HTTP %d", resp.StatusCode)
		log.WarnContext(ctx, "update latest release status failed", "repo", repo, "status_code", resp.StatusCode, "err", err)
		return release{}, err
	}
	var latest release
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		log.WarnContext(ctx, "update latest release decode failed", "repo", repo, "err", err)
		return release{}, fmt.Errorf("decode latest release: %w", err)
	}
	if latest.Draft || latest.Prerelease {
		err := fmt.Errorf("latest release %q is not an allowed stable release", latest.TagName)
		log.WarnContext(ctx, "update latest release rejected", "repo", repo, "tag", latest.TagName, "err", err)
		return release{}, err
	}
	return latest, nil
}

func fetchReleaseList(ctx context.Context, options Options, repo string) ([]release, error) {
	log := options.Log
	url := releaseAPIBaseURL(options.Config) + "/repos/" + repo + "/releases"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.WarnContext(ctx, "update release list request build failed", "repo", repo, "err", err)
		return nil, fmt.Errorf("build release list request: %w", err)
	}
	applyGitHubAPIHeaders(req, options.Config)
	resp, err := options.Client.Do(req)
	if err != nil {
		log.WarnContext(ctx, "update release list request failed", "repo", repo, "err", err)
		return nil, fmt.Errorf("query releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("query releases: HTTP %d", resp.StatusCode)
		log.WarnContext(ctx, "update release list status failed", "repo", repo, "status_code", resp.StatusCode, "err", err)
		return nil, err
	}
	var releases []release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		log.WarnContext(ctx, "update release list decode failed", "repo", repo, "err", err)
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	return releases, nil
}

// VerifyReleaseAssets downloads every <binary>_<os>_<arch>.tar.gz asset of the
// release tagged tag and verifies its checksum and GitHub attestations.
func VerifyReleaseAssets(ctx context.Context, options Options, tag string) error {
	resolvedOptions := resolveOptions(options)
	if err := validateReleaseVerificationInput(resolvedOptions.Config, tag); err != nil {
		return err
	}
	latest, err := fetchReleaseByTag(ctx, resolvedOptions, tag)
	if err != nil {
		return err
	}
	assets := releaseVerificationAssets(latest.Assets, resolvedOptions.Config.Binary)
	if len(assets) == 0 {
		return fmt.Errorf("no release assets matched %s_*.tar.gz in %s", resolvedOptions.Config.Binary, tag)
	}
	if err := os.MkdirAll(resolvedOptions.CacheDir, 0o700); err != nil {
		resolvedOptions.Log.WarnContext(ctx, "release verification cache dir create failed", "path", resolvedOptions.CacheDir, "err", err)
		return fmt.Errorf("create release verification cache dir: %w", err)
	}
	for _, asset := range assets {
		if asset.BrowserDownloadURL == "" {
			return fmt.Errorf("release asset %s has no download URL", asset.Name)
		}
		archivePath := filepath.Join(resolvedOptions.CacheDir, filepath.Base(asset.Name))
		if err := updateDownloadFile(ctx, resolvedOptions.Client, asset.BrowserDownloadURL, archivePath); err != nil {
			return err
		}
		if err := updateVerifyChecksum(ctx, resolvedOptions, latest, asset, archivePath); err != nil {
			return err
		}
		if err := updateVerifyGitHubAttestations(ctx, resolvedOptions, latest, asset, archivePath); err != nil {
			return err
		}
		logVerifiedReleaseAsset(ctx, resolvedOptions, latest, asset)
	}
	return nil
}

func logVerifiedReleaseAsset(ctx context.Context, options Options, latest release, asset releaseAsset) {
	options.Log.InfoContext(ctx, "release asset verified", "asset", asset.Name, "tag", latest.TagName)
}

func validateReleaseVerificationInput(cfg Config, tag string) error {
	if strings.TrimSpace(cfg.Repo) == "" {
		return fmt.Errorf("update repo is required")
	}
	if strings.TrimSpace(cfg.Binary) == "" {
		return fmt.Errorf("update binary is required")
	}
	if strings.TrimSpace(tag) == "" {
		return fmt.Errorf("release tag is required")
	}
	return nil
}

func fetchReleaseByTag(ctx context.Context, options Options, tag string) (release, error) {
	log := options.Log
	repo := options.Config.Repo
	requestURL := releaseAPIBaseURL(options.Config) + "/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		log.WarnContext(ctx, "update tagged release request build failed", "repo", repo, "tag", tag, "err", err)
		return release{}, fmt.Errorf("build tagged release request: %w", err)
	}
	applyGitHubAPIHeaders(req, options.Config)
	resp, err := options.Client.Do(req)
	if err != nil {
		log.WarnContext(ctx, "update tagged release request failed", "repo", repo, "tag", tag, "err", err)
		return release{}, fmt.Errorf("query tagged release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("query tagged release: HTTP %d", resp.StatusCode)
		log.WarnContext(ctx, "update tagged release status failed", "repo", repo, "tag", tag, "status_code", resp.StatusCode, "err", err)
		return release{}, err
	}
	var tagged release
	if err := json.NewDecoder(resp.Body).Decode(&tagged); err != nil {
		log.WarnContext(ctx, "update tagged release decode failed", "repo", repo, "tag", tag, "err", err)
		return release{}, fmt.Errorf("decode tagged release: %w", err)
	}
	return tagged, nil
}

func releaseVerificationAssets(assets []releaseAsset, binary string) []releaseAsset {
	matches := []releaseAsset{}
	prefix := binary + "_"
	for _, asset := range assets {
		if !strings.HasPrefix(asset.Name, prefix) {
			continue
		}
		if !strings.HasSuffix(asset.Name, ".tar.gz") {
			continue
		}
		matches = append(matches, asset)
	}
	return matches
}

func selectArchiveAsset(assets []releaseAsset, binary string) (releaseAsset, error) {
	name := fmt.Sprintf("%s_%s_%s.tar.gz", binary, runtime.GOOS, runtime.GOARCH)
	for _, asset := range assets {
		if asset.Name == name && asset.BrowserDownloadURL != "" {
			return asset, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("release asset %q not found", name)
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name && asset.BrowserDownloadURL != "" {
			return asset, true
		}
	}
	return releaseAsset{Name: "", BrowserDownloadURL: "", Digest: ""}, false
}

func applyGitHubAPIHeaders(req *http.Request, cfg Config) {
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := strings.TrimSpace(cfg.AuthToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func releaseAPIBaseURL(cfg Config) string {
	override := ""
	if cfg.APIBaseURLEnv != "" {
		override = strings.TrimSpace(os.Getenv(cfg.APIBaseURLEnv))
	}
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	if cfg.APIBaseURL != "" {
		return strings.TrimRight(cfg.APIBaseURL, "/")
	}
	return defaultGitHubAPIBaseURL
}

func releaseIsNewer(currentVersion string, latestTag string) bool {
	if latestTag == "" || latestTag == currentVersion {
		return false
	}
	if semver.IsValid(currentVersion) && semver.IsValid(latestTag) {
		return semver.Compare(latestTag, currentVersion) > 0
	}
	currentTimestamp := versionTimestampPrefix(currentVersion)
	latestTimestamp := versionTimestampPrefix(latestTag)
	if currentTimestamp != "" && latestTimestamp != "" {
		return latestTimestamp > currentTimestamp
	}
	if latestTimestamp != "" && (currentVersion == "dev" || currentVersion == "unknown") {
		return true
	}
	return true
}

func versionTimestampPrefix(value string) string {
	if len(value) < 12 {
		return ""
	}
	prefix := value[:12]
	for _, char := range prefix {
		if char < '0' || char > '9' {
			return ""
		}
	}
	return prefix
}
