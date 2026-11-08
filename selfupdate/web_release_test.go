package selfupdate_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"goodkind.io/go-makefile/selfupdate"
)

const (
	webTestRepo       = "agoodkind/agent-gate"
	webTestRollingTag = "202611031628-101-035da31"
	webTestStableTag  = "v1.2.3"
	webTestOldVersion = "202601010000-1-0000000"
	webTestAssetName  = "agent-gate_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	webTestArchive    = "archive bytes"
	// webTestRealBundle is the build provenance bundle of the
	// agent-gate_darwin_arm64.tar.gz archive in agent-gate release
	// 202611031628-101-035da31.
	webTestRealBundle = "testdata/agent-gate_darwin_arm64.tar.gz.sigstore.json"
)

type webRelease struct {
	server             *httptest.Server
	mutex              sync.Mutex
	paths              []string
	authorizations     []string
	feedTag            string
	additionalEntries  string
	stableTag          string
	bundle             []byte
	checksumNamePrefix string
}

func newWebRelease(t *testing.T, feedTag string, stableTag string, bundle []byte) *webRelease {
	t.Helper()
	release := &webRelease{feedTag: feedTag, stableTag: stableTag, bundle: bundle}
	release.server = httptest.NewServer(http.HandlerFunc(release.serve))
	t.Cleanup(release.server.Close)

	return release
}

func (release *webRelease) serve(writer http.ResponseWriter, request *http.Request) {
	release.mutex.Lock()
	release.paths = append(release.paths, request.URL.Path)
	release.authorizations = append(release.authorizations, request.Header.Get("Authorization"))
	release.mutex.Unlock()
	repoPath := "/" + webTestRepo + "/releases"
	switch request.URL.Path {
	case repoPath + ".atom":
		feed := `<feed xmlns="http://www.w3.org/2005/Atom"><id>tag:github.com,2008:` + repoPath + `</id>` +
			release.additionalEntries +
			`<entry><id>tag:github.com,2008:Repository/1/` + release.feedTag + `</id>` +
			`<updated>2026-11-03T16:28:00Z</updated></entry></feed>`
		_, _ = writer.Write([]byte(feed))

		return
	case repoPath + "/latest":
		location := repoPath
		if release.stableTag != "" {
			location = repoPath + "/tag/" + release.stableTag
		}

		http.Redirect(writer, request, location, http.StatusFound)

		return
	}

	for _, tag := range []string{release.feedTag, release.stableTag} {
		release.serveAsset(writer, request, repoPath+"/download/"+tag+"/")
	}
}

func (release *webRelease) serveAsset(writer http.ResponseWriter, request *http.Request, prefix string) {
	assetName, found := strings.CutPrefix(request.URL.Path, prefix)
	if !found {
		return
	}

	switch assetName {
	case "checksums.txt":
		digest := sha256.Sum256([]byte(webTestArchive))
		_, _ = writer.Write([]byte(hex.EncodeToString(digest[:]) + "  " + release.checksumNamePrefix + webTestAssetName + "\n"))
	case webTestAssetName:
		_, _ = writer.Write([]byte(webTestArchive))
	case webTestAssetName + ".sigstore.json":
		if release.bundle == nil {
			http.NotFound(writer, request)
			return
		}

		_, _ = writer.Write(release.bundle)
	default:
		http.NotFound(writer, request)
	}
}

func (release *webRelease) assertNoAPIRequests(t *testing.T) {
	t.Helper()
	release.mutex.Lock()
	defer release.mutex.Unlock()
	if len(release.paths) == 0 {
		t.Fatal("server did not receive any requests")
	}

	for _, path := range release.paths {
		if strings.HasPrefix(path, "/repos/") {
			t.Errorf("server received API request %s", path)
		}
	}

	for _, authorization := range release.authorizations {
		if authorization != "" {
			t.Error("server received an Authorization header")
		}
	}
}

func webOptions(t *testing.T, release *webRelease, allowPrerelease bool) selfupdate.Options {
	t.Helper()
	stateDir := t.TempDir()
	installPath := filepath.Join(t.TempDir(), "agent-gate")
	if err := os.WriteFile(installPath, []byte("installed"), 0o700); err != nil {
		t.Fatalf("write installed binary: %v", err)
	}

	return selfupdate.Options{
		Config: selfupdate.Config{
			Repo:             webTestRepo,
			Binary:           "agent-gate",
			CurrentVersion:   webTestOldVersion,
			CurrentCommit:    "0000000",
			CurrentBuildHash: "buildhash",
			AllowPrerelease:  &allowPrerelease,
			WebBaseURL:       release.server.URL,
			AuthToken:        "retained-token",
		},
		Client:      release.server.Client(),
		InstallPath: installPath,
		CacheDir:    filepath.Join(stateDir, "cache"),
		StatePath:   filepath.Join(stateDir, "update.json"),
	}
}

func assertInstalledUnchanged(t *testing.T, options selfupdate.Options) {
	t.Helper()
	content, err := os.ReadFile(options.InstallPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}

	if string(content) != "installed" {
		t.Fatalf("installed binary = %q, want unchanged", content)
	}
}

func TestCheckFindsRollingReleaseInFeedWithoutGitHubAPI(t *testing.T) {
	release := newWebRelease(t, webTestRollingTag, webTestStableTag, nil)

	result, err := selfupdate.Check(context.Background(), webOptions(t, release, true))
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	if result.LatestTag != webTestRollingTag || result.AssetName != webTestAssetName || !result.UpdateAvailable {
		t.Fatalf("Check() = %+v, want %s and %s with an update", result, webTestRollingTag, webTestAssetName)
	}

	release.assertNoAPIRequests(t)
}

func TestCheckSelectsNewestReleaseWhenFeedStartsWithOlderTag(t *testing.T) {
	release := newWebRelease(t, webTestRollingTag, webTestStableTag, nil)
	release.additionalEntries = `<entry><id>tag:github.com,2008:Repository/1/staticcheck/v0.2.0</id>` +
		`<updated>2026-11-03T15:00:00Z</updated></entry>`

	result, err := selfupdate.Check(context.Background(), webOptions(t, release, true))
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if result.LatestTag != webTestRollingTag || result.AssetName != webTestAssetName {
		t.Fatalf("Check() = %+v, want %s and %s", result, webTestRollingTag, webTestAssetName)
	}
	release.assertNoAPIRequests(t)
}

func TestCheckRejectsFeedEntryWithoutUpdateTime(t *testing.T) {
	release := newWebRelease(t, webTestRollingTag, webTestStableTag, nil)
	release.additionalEntries = `<entry><id>tag:github.com,2008:Repository/1/staticcheck/v0.2.0</id></entry>`

	_, err := selfupdate.Check(context.Background(), webOptions(t, release, true))
	if err == nil || !strings.Contains(err.Error(), "missing its update time") {
		t.Fatalf("Check() error = %v, want a missing update time error", err)
	}
}

func TestCheckFindsStableReleaseInRedirectThroughWebBaseEnv(t *testing.T) {
	release := newWebRelease(t, webTestRollingTag, webTestStableTag, nil)
	t.Setenv("SELFUPDATE_TEST_WEB_BASE", release.server.URL)
	options := webOptions(t, release, false)
	options.Config.WebBaseURL = "http://127.0.0.1:1"
	options.Config.WebBaseURLEnv = "SELFUPDATE_TEST_WEB_BASE"

	result, err := selfupdate.Check(context.Background(), options)
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	if result.LatestTag != webTestStableTag {
		t.Fatalf("LatestTag = %q, want %q", result.LatestTag, webTestStableTag)
	}

	release.assertNoAPIRequests(t)
}

// Agent Gate release 202606060459-4b-9822954 prefixes checksum names with ./.
func TestCheckReadsChecksumNamesWithDotSlashPrefix(t *testing.T) {
	release := newWebRelease(t, webTestRollingTag, webTestStableTag, nil)
	release.checksumNamePrefix = "./"

	result, err := selfupdate.Check(context.Background(), webOptions(t, release, false))
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	if result.AssetName != webTestAssetName {
		t.Fatalf("AssetName = %q, want %q", result.AssetName, webTestAssetName)
	}
}

func TestCheckReportsRepositoryWithoutStableRelease(t *testing.T) {
	release := newWebRelease(t, webTestRollingTag, "", nil)

	_, err := selfupdate.Check(context.Background(), webOptions(t, release, false))
	if err == nil || !strings.Contains(err.Error(), "no stable release") {
		t.Fatalf("Check() error = %v, want no stable release", err)
	}

	release.assertNoAPIRequests(t)
}

func TestApplyRejectsReleaseWithoutBundle(t *testing.T) {
	release := newWebRelease(t, webTestRollingTag, webTestStableTag, nil)
	options := webOptions(t, release, true)
	var logOutput strings.Builder
	options.Log = slog.New(slog.NewTextHandler(&logOutput, nil))

	_, err := selfupdate.Apply(context.Background(), options)
	if !strings.Contains(logOutput.String(), "update build provenance bundle request failed") {
		t.Fatalf("caller logger did not record the bundle request failure: %s", logOutput.String())
	}

	if err == nil || !strings.Contains(err.Error(), "download build provenance bundle") || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("Apply() error = %v, want a missing bundle error", err)
	}

	assertInstalledUnchanged(t, options)
	release.assertNoAPIRequests(t)
}

// The archive passes its checksum but does not match the signed bundle.
func TestApplyRejectsArchiveThatRealBundleDoesNotCover(t *testing.T) {
	bundle, err := os.ReadFile(webTestRealBundle)
	if err != nil {
		t.Fatalf("read real bundle: %v", err)
	}

	release := newWebRelease(t, webTestRollingTag, webTestStableTag, bundle)
	options := webOptions(t, release, true)

	_, err = selfupdate.Apply(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "provided artifact digest does not match") {
		t.Fatalf("Apply() error = %v, want an artifact digest mismatch", err)
	}

	assertInstalledUnchanged(t, options)
	release.assertNoAPIRequests(t)
}
