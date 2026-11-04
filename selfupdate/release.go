package selfupdate

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	checksumsAssetName     = "checksums.txt"
	maxReleaseMetadataSize = 16 << 20
	releaseFeedIDMarker    = ":Repository/"
	releaseTagPathMarker   = "/releases/tag/"
)

type release struct {
	HTMLURL string
	TagName string
	Assets  []releaseAsset
}

type releaseAsset struct {
	Name               string
	BrowserDownloadURL string
}

type releaseFeed struct {
	Entries []releaseFeedEntry `xml:"entry"`
}

type releaseFeedEntry struct {
	ID string `xml:"id"`
}

// fetchLatestRelease resolves the latest allowed tag from GitHub release pages
// and reads the release assets from the checksums.txt asset of that tag.
func fetchLatestRelease(ctx context.Context, options Options) (release, error) {
	resolveTag := fetchLatestStableTag
	if options.Config.allowPrerelease() {
		resolveTag = fetchNewestFeedTag
	}
	tag, err := resolveTag(ctx, options)
	if err != nil {
		return release{}, err
	}
	return fetchReleaseByTag(ctx, options, tag)
}

// fetchNewestFeedTag returns the tag of the first entry in the release feed. A
// feed entry has an id such as tag:github.com,2008:Repository/1/TAG.
func fetchNewestFeedTag(ctx context.Context, options Options) (string, error) {
	log := options.Log
	repo := options.Config.Repo
	feedURL := releaseWebBaseURL(options.Config) + "/" + repo + "/releases.atom"
	body, err := fetchReleaseMetadata(ctx, options, feedURL)
	if err != nil {
		log.WarnContext(ctx, "update release feed query failed", "repo", repo, "err", err)
		return "", fmt.Errorf("query release feed: %w", err)
	}
	var feed releaseFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		log.WarnContext(ctx, "update release feed decode failed", "repo", repo, "err", err)
		return "", fmt.Errorf("decode release feed: %w", err)
	}
	if len(feed.Entries) == 0 {
		noReleaseErr := fmt.Errorf("no non-draft releases found for %s", repo)
		log.WarnContext(ctx, "update release feed had no entry", "repo", repo, "err", noReleaseErr)
		return "", noReleaseErr
	}
	_, entryPath, found := strings.Cut(feed.Entries[0].ID, releaseFeedIDMarker)
	if !found {
		return "", fmt.Errorf("release feed entry id %q has no repository path", feed.Entries[0].ID)
	}
	_, tag, found := strings.Cut(entryPath, "/")
	if !found || tag == "" {
		return "", fmt.Errorf("release feed entry id %q has no tag", feed.Entries[0].ID)
	}
	return tag, nil
}

// fetchLatestStableTag reads the tag from the redirect of the releases/latest
// page. GitHub redirects that page to the latest stable release, or to the
// release list when the repository has no stable release.
func fetchLatestStableTag(ctx context.Context, options Options) (string, error) {
	log := options.Log
	repo := options.Config.Repo
	latestURL := releaseWebBaseURL(options.Config) + "/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		log.WarnContext(ctx, "update latest release request build failed", "repo", repo, "err", err)
		return "", fmt.Errorf("build latest release request: %w", err)
	}
	client := *options.Client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		log.WarnContext(ctx, "update latest release request failed", "repo", repo, "err", err)
		return "", fmt.Errorf("query latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	location, err := resp.Location()
	if err != nil {
		statusErr := fmt.Errorf("query latest release: HTTP %d without a redirect", resp.StatusCode)
		log.WarnContext(ctx, "update latest release status failed", "repo", repo, "status_code", resp.StatusCode, "err", errors.Join(statusErr, err))
		return "", statusErr
	}
	_, tag, found := strings.Cut(location.Path, releaseTagPathMarker)
	if !found || tag == "" {
		noReleaseErr := fmt.Errorf("no stable release found for %s", repo)
		log.WarnContext(ctx, "update latest release redirect had no tag", "repo", repo, "location", location.String(), "err", noReleaseErr)
		return "", noReleaseErr
	}
	return tag, nil
}

func fetchReleaseMetadata(ctx context.Context, options Options, requestURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		options.Log.WarnContext(ctx, "update release metadata request build failed", "url", requestURL, "err", err)
		return nil, fmt.Errorf("build request for %s: %w", requestURL, err)
	}
	resp, err := options.Client.Do(req)
	if err != nil {
		options.Log.WarnContext(ctx, "update release metadata request failed", "url", requestURL, "err", err)
		return nil, fmt.Errorf("request %s: %w", requestURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("request %s: HTTP %d", requestURL, resp.StatusCode)
		options.Log.WarnContext(ctx, "update release metadata status failed", "url", requestURL, "status_code", resp.StatusCode, "err", err)
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseMetadataSize))
	if err != nil {
		options.Log.WarnContext(ctx, "update release metadata read failed", "url", requestURL, "err", err)
		return nil, fmt.Errorf("read %s: %w", requestURL, err)
	}
	return body, nil
}

// VerifyReleaseAssets downloads every archive asset of the release tagged tag
// and verifies its checksum and GitHub attestations. It also requires the named
// binary to be present so a misconfigured caller fails loudly.
func VerifyReleaseAssets(ctx context.Context, options Options, tag string) error {
	resolvedOptions := resolveOptions(options)
	if err := validateReleaseVerificationInput(resolvedOptions.Config, tag); err != nil {
		return err
	}
	latest, err := fetchReleaseByTag(ctx, resolvedOptions, tag)
	if err != nil {
		return err
	}
	if missing := missingRequiredAssets(latest.Assets, resolvedOptions.RequiredAssets); len(missing) > 0 {
		return fmt.Errorf("release %s lacks required assets: %s", tag, strings.Join(missing, ", "))
	}
	if !releaseHasNamedBinaryAssets(latest.Assets, resolvedOptions.Config.Binary) {
		return fmt.Errorf("no release assets matched %s_*.tar.gz in %s", resolvedOptions.Config.Binary, tag)
	}
	assets := releaseVerificationAssets(latest.Assets)
	if err := os.MkdirAll(resolvedOptions.CacheDir, 0o700); err != nil {
		resolvedOptions.Log.WarnContext(ctx, "release verification cache dir create failed", "path", resolvedOptions.CacheDir, "err", err)
		return fmt.Errorf("create release verification cache dir: %w", err)
	}
	for _, asset := range assets {
		archivePath := filepath.Join(resolvedOptions.CacheDir, filepath.Base(asset.Name))
		if err := downloadReleaseAsset(ctx, resolvedOptions, asset, archivePath); err != nil {
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

// fetchReleaseByTag lists the release assets from the checksums.txt asset of
// the tag. That file has one line for every archive in the release.
func fetchReleaseByTag(ctx context.Context, options Options, tag string) (release, error) {
	cfg := options.Config
	checksumsURL := releaseAssetURL(cfg, tag, checksumsAssetName)
	body, err := fetchReleaseMetadata(ctx, options, checksumsURL)
	if err != nil {
		options.Log.WarnContext(ctx, "update tagged release query failed", "repo", cfg.Repo, "tag", tag, "err", err)
		return release{}, fmt.Errorf("query tagged release: %w", err)
	}
	assets := []releaseAsset{{Name: checksumsAssetName, BrowserDownloadURL: checksumsURL}}
	for line := range strings.SplitSeq(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := checksumEntryName(fields[1])
		assets = append(assets, releaseAsset{
			Name:               name,
			BrowserDownloadURL: releaseAssetURL(cfg, tag, name),
		})
	}
	return release{
		HTMLURL: releaseWebBaseURL(cfg) + "/" + cfg.Repo + releaseTagPathMarker + url.PathEscape(tag),
		TagName: tag,
		Assets:  assets,
	}, nil
}

func releaseVerificationAssets(assets []releaseAsset) []releaseAsset {
	matches := []releaseAsset{}
	for _, asset := range assets {
		if !strings.HasSuffix(asset.Name, ".tar.gz") {
			continue
		}
		matches = append(matches, asset)
	}
	return matches
}

func missingRequiredAssets(assets []releaseAsset, required []string) []string {
	missing := []string{}
	present := make(map[string]bool, len(assets))
	for _, asset := range assets {
		present[asset.Name] = true
	}
	for _, name := range required {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

func releaseHasNamedBinaryAssets(assets []releaseAsset, binary string) bool {
	prefix := binary + "_"
	for _, asset := range assets {
		if !strings.HasPrefix(asset.Name, prefix) {
			continue
		}
		if strings.HasSuffix(asset.Name, ".tar.gz") {
			return true
		}
	}
	return false
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
	return releaseAsset{Name: "", BrowserDownloadURL: ""}, false
}

func releaseWebBaseURL(cfg Config) string {
	override := ""
	if cfg.WebBaseURLEnv != "" {
		override = strings.TrimSpace(os.Getenv(cfg.WebBaseURLEnv))
	}
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	if cfg.WebBaseURL != "" {
		return strings.TrimRight(cfg.WebBaseURL, "/")
	}
	return defaultGitHubWebBaseURL
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
