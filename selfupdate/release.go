package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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
	req.Header.Set("Accept", "application/vnd.github+json")
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
	log := slog.Default()
	url := releaseAPIBaseURL(options.Config) + "/repos/" + repo + "/releases"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.WarnContext(ctx, "update release list request build failed", "repo", repo, "err", err)
		return nil, fmt.Errorf("build release list request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
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
