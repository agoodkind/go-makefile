package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testCurrentVersion = "202606240000-aa-abcdef0"

func testConfig() Config {
	return Config{
		Repo:             "agoodkind/agent-gate",
		Binary:           "agent-gate",
		CurrentVersion:   testCurrentVersion,
		CurrentCommit:    "abcdef0",
		CurrentBuildHash: "buildhash",
	}
}

func TestSelectArchiveAssetMatchesRuntimePlatform(t *testing.T) {
	runtimeAssetName := "agent-gate_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	assets := []releaseAsset{
		{Name: runtimeAssetName, BrowserDownloadURL: "https://example.invalid/runtime"},
		{Name: "agent-gate_other_other.tar.gz", BrowserDownloadURL: "https://example.invalid/other"},
	}

	asset, err := selectArchiveAsset(assets, "agent-gate")
	if err != nil {
		t.Fatalf("selectArchiveAsset() error: %v", err)
	}
	if asset.Name != runtimeAssetName {
		t.Fatalf("asset name = %q, want %q", asset.Name, runtimeAssetName)
	}
}

func TestChecksumFromAsset(t *testing.T) {
	asset := releaseAsset{Digest: "sha256:abc123"}
	if got := checksumFromAsset(asset); got != "abc123" {
		t.Fatalf("checksumFromAsset() = %q, want %q", got, "abc123")
	}
}

func TestChecksumFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checksums.txt")
	content := "abc123  agent-gate_darwin_arm64.tar.gz\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write checksums: %v", err)
	}
	got, err := checksumFromFile(path, "agent-gate_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("checksumFromFile() error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("checksumFromFile() = %q, want %q", got, "abc123")
	}
}

func TestVerifyReleaseAssetsDownloadsAndVerifiesMatchingArchives(t *testing.T) {
	originalVerifyGitHubAttestations := updateVerifyGitHubAttestations
	originalExtractCandidate := updateExtractCandidate
	originalValidateCandidate := updateValidateCandidate
	t.Cleanup(func() {
		updateVerifyGitHubAttestations = originalVerifyGitHubAttestations
		updateExtractCandidate = originalExtractCandidate
		updateValidateCandidate = originalValidateCandidate
	})

	darwinAssetName := "agent-gate_darwin_amd64.tar.gz"
	linuxAssetName := "agent-gate_linux_arm64.tar.gz"
	siblingAssetName := "agentctl_linux_arm64.tar.gz"
	darwinArchive := []byte("darwin archive")
	linuxArchive := []byte("linux archive")
	siblingArchive := []byte("sibling archive")
	checksums := fmt.Sprintf(
		"%s  %s\n%s  %s\n%s  %s\n",
		testSHA256Hex(darwinArchive),
		darwinAssetName,
		testSHA256Hex(linuxArchive),
		linuxAssetName,
		testSHA256Hex(siblingArchive),
		siblingAssetName,
	)
	verifiedAttestations := []string{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/agoodkind/agent-gate/releases/tags/v1.2.3":
			if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q, want bearer token on API request", got)
			}
			response := release{
				TagName: "v1.2.3",
				Assets: []releaseAsset{
					{
						Name:               darwinAssetName,
						BrowserDownloadURL: server.URL + "/downloads/" + darwinAssetName,
					},
					{
						Name:               linuxAssetName,
						BrowserDownloadURL: server.URL + "/downloads/" + linuxAssetName,
					},
					{
						Name:               siblingAssetName,
						BrowserDownloadURL: server.URL + "/downloads/" + siblingAssetName,
					},
					{
						Name:               "checksums.txt",
						BrowserDownloadURL: server.URL + "/downloads/checksums.txt",
					},
					{
						Name:               "agent-gate-notes.txt",
						BrowserDownloadURL: server.URL + "/downloads/notes.txt",
					},
				},
			}
			if err := json.NewEncoder(writer).Encode(response); err != nil {
				t.Errorf("encode response: %v", err)
			}
		case "/downloads/" + darwinAssetName:
			if got := request.Header.Get("Authorization"); got != "" {
				t.Errorf("Authorization = %q, want no token on asset download", got)
			}
			_, _ = writer.Write(darwinArchive)
		case "/downloads/" + linuxAssetName:
			if got := request.Header.Get("Authorization"); got != "" {
				t.Errorf("Authorization = %q, want no token on asset download", got)
			}
			_, _ = writer.Write(linuxArchive)
		case "/downloads/" + siblingAssetName:
			if got := request.Header.Get("Authorization"); got != "" {
				t.Errorf("Authorization = %q, want no token on asset download", got)
			}
			_, _ = writer.Write(siblingArchive)
		case "/downloads/checksums.txt":
			if got := request.Header.Get("Authorization"); got != "" {
				t.Errorf("Authorization = %q, want no token on checksum download", got)
			}
			_, _ = writer.Write([]byte(checksums))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	updateVerifyGitHubAttestations = func(_ context.Context, _ Options, _ release, asset releaseAsset, _ string) error {
		verifiedAttestations = append(verifiedAttestations, asset.Name)
		return nil
	}
	updateExtractCandidate = func(_ string, _ string) (string, func(), error) {
		t.Fatal("updateExtractCandidate() should not run during release verification")
		return "", func() {}, nil
	}
	updateValidateCandidate = func(_ context.Context, _ Config, _ string) error {
		t.Fatal("updateValidateCandidate() should not run during release verification")
		return nil
	}

	cacheDir := t.TempDir()
	options := Options{
		Config: Config{
			Repo:       "agoodkind/agent-gate",
			Binary:     "agent-gate",
			APIBaseURL: server.URL,
			AuthToken:  "test-token",
		},
		Client:   server.Client(),
		CacheDir: cacheDir,
	}

	err := VerifyReleaseAssets(context.Background(), options, "v1.2.3")
	if err != nil {
		t.Fatalf("VerifyReleaseAssets() error: %v", err)
	}
	if strings.Join(verifiedAttestations, ",") != darwinAssetName+","+linuxAssetName+","+siblingAssetName {
		t.Fatalf("verified attestations = %#v", verifiedAttestations)
	}
	assertFileBytes(t, filepath.Join(cacheDir, darwinAssetName), darwinArchive)
	assertFileBytes(t, filepath.Join(cacheDir, linuxAssetName), linuxArchive)
	assertFileBytes(t, filepath.Join(cacheDir, siblingAssetName), siblingArchive)
}

func TestVerifyReleaseAssetsRequiresMatchingArchives(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/agoodkind/agent-gate/releases/tags/v1.2.3" {
			http.NotFound(writer, request)
			return
		}
		response := release{
			TagName: "v1.2.3",
			Assets: []releaseAsset{
				{
					Name:               "checksums.txt",
					BrowserDownloadURL: server.URL + "/downloads/checksums.txt",
				},
			},
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	options := Options{
		Config: Config{
			Repo:       "agoodkind/agent-gate",
			Binary:     "agent-gate",
			APIBaseURL: server.URL,
		},
		Client:   server.Client(),
		CacheDir: t.TempDir(),
	}

	err := VerifyReleaseAssets(context.Background(), options, "v1.2.3")
	if err == nil {
		t.Fatal("VerifyReleaseAssets() error = nil, want missing archive error")
	}
	if !strings.Contains(err.Error(), "no release assets matched") {
		t.Fatalf("VerifyReleaseAssets() error = %v", err)
	}
}

func TestVerifyReleaseAssetsRequiresNamedBinaryAmongAllArchives(t *testing.T) {
	archive := []byte("sibling archive")
	assetName := "agentctl_linux_arm64.tar.gz"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/agoodkind/agent-gate/releases/tags/v1.2.3":
			response := release{
				TagName: "v1.2.3",
				Assets: []releaseAsset{
					{
						Name:               assetName,
						BrowserDownloadURL: server.URL + "/downloads/" + assetName,
						Digest:             "sha256:" + testSHA256Hex(archive),
					},
				},
			}
			if err := json.NewEncoder(writer).Encode(response); err != nil {
				t.Errorf("encode response: %v", err)
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	options := Options{
		Config: Config{
			Repo:       "agoodkind/agent-gate",
			Binary:     "agent-gate",
			APIBaseURL: server.URL,
		},
		Client:   server.Client(),
		CacheDir: t.TempDir(),
	}

	err := VerifyReleaseAssets(context.Background(), options, "v1.2.3")
	if err == nil {
		t.Fatal("VerifyReleaseAssets() error = nil, want missing named binary error")
	}
	if !strings.Contains(err.Error(), "no release assets matched agent-gate_*.tar.gz") {
		t.Fatalf("VerifyReleaseAssets() error = %v", err)
	}
}

func TestVerifyReleaseAssetsKeepsDownloadsInsideCacheDir(t *testing.T) {
	originalVerifyGitHubAttestations := updateVerifyGitHubAttestations
	t.Cleanup(func() {
		updateVerifyGitHubAttestations = originalVerifyGitHubAttestations
	})

	archive := []byte("archive")
	assetName := "agent-gate_/../../evil.tar.gz"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/agoodkind/agent-gate/releases/tags/v1.2.3":
			response := release{
				TagName: "v1.2.3",
				Assets: []releaseAsset{
					{
						Name:               assetName,
						BrowserDownloadURL: server.URL + "/downloads/evil.tar.gz",
						Digest:             "sha256:" + testSHA256Hex(archive),
					},
				},
			}
			if err := json.NewEncoder(writer).Encode(response); err != nil {
				t.Errorf("encode response: %v", err)
			}
		case "/downloads/evil.tar.gz":
			_, _ = writer.Write(archive)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	updateVerifyGitHubAttestations = func(_ context.Context, _ Options, _ release, _ releaseAsset, _ string) error {
		return nil
	}

	parentDir := t.TempDir()
	cacheDir := filepath.Join(parentDir, "cache")
	options := Options{
		Config: Config{
			Repo:       "agoodkind/agent-gate",
			Binary:     "agent-gate",
			APIBaseURL: server.URL,
		},
		Client:   server.Client(),
		CacheDir: cacheDir,
	}

	err := VerifyReleaseAssets(context.Background(), options, "v1.2.3")
	if err != nil {
		t.Fatalf("VerifyReleaseAssets() error: %v", err)
	}
	assertFileBytes(t, filepath.Join(cacheDir, "evil.tar.gz"), archive)
	if _, statErr := os.Stat(filepath.Join(parentDir, "evil.tar.gz")); !os.IsNotExist(statErr) {
		t.Fatalf("download escaped cache dir; stat outside path = %v", statErr)
	}
}

func TestFetchReleaseListUsesOptionsLogger(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "failure", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	var logOutput strings.Builder
	options := Options{
		Config: Config{
			APIBaseURL: server.URL,
		},
		Client: server.Client(),
		Log:    slog.New(slog.NewTextHandler(&logOutput, nil)),
	}

	_, err := fetchReleaseList(context.Background(), options, "agoodkind/agent-gate")
	if err == nil {
		t.Fatal("fetchReleaseList() error = nil, want status error")
	}
	if !strings.Contains(logOutput.String(), "update release list status failed") {
		t.Fatalf("options logger output = %q, want release list status log", logOutput.String())
	}
}

func TestResolveOptionsDefaults(t *testing.T) {
	options := resolveOptions(Options{Config: testConfig()})
	if options.Client == nil {
		t.Fatal("Client = nil, want default client")
	}
	if options.Client.Timeout != defaultHTTPTimeout {
		t.Fatalf("Client.Timeout = %s, want %s", options.Client.Timeout, defaultHTTPTimeout)
	}
	if !strings.Contains(options.CacheDir, "agent-gate") {
		t.Fatalf("CacheDir = %q, want agent-gate path", options.CacheDir)
	}
	if !strings.Contains(options.StatePath, "agent-gate") {
		t.Fatalf("StatePath = %q, want agent-gate path", options.StatePath)
	}
	if options.Config.ValidateArgs[0] != "version" {
		t.Fatalf("ValidateArgs = %#v, want version default", options.Config.ValidateArgs)
	}
	if options.Config.ValidateMatch != "version:" {
		t.Fatalf("ValidateMatch = %q, want version:", options.Config.ValidateMatch)
	}
	if !options.Config.allowPrerelease() {
		t.Fatal("AllowPrerelease default = false, want true")
	}
}

func TestReleaseIsNewer(t *testing.T) {
	testCases := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "equal", current: "v1.2.3", latest: "v1.2.3", want: false},
		{name: "semver newer", current: "v1.2.3", latest: "v1.2.4", want: true},
		{name: "semver older", current: "v1.2.4", latest: "v1.2.3", want: false},
		{name: "timestamp newer", current: "202606210601-6a-a2d8820-2-g2c1e52b-dirty", latest: "202606220101-ab-1234567", want: true},
		{name: "timestamp older", current: "202606210601-6a-a2d8820-2-g2c1e52b-dirty", latest: "202606060459-4b-9822954", want: false},
		{name: "dev current", current: "dev", latest: "202606060459-4b-9822954", want: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := releaseIsNewer(testCase.current, testCase.latest)
			if got != testCase.want {
				t.Fatalf("releaseIsNewer(%q, %q) = %t, want %t", testCase.current, testCase.latest, got, testCase.want)
			}
		})
	}
}

func TestApplyDryRunIsIdempotent(t *testing.T) {
	originalWithLock := updateWithLock
	originalFetchLatestRelease := updateFetchLatestRelease
	originalDownloadFile := updateDownloadFile
	originalVerifyChecksum := updateVerifyChecksum
	originalVerifyGitHubAttestations := updateVerifyGitHubAttestations
	originalExtractCandidate := updateExtractCandidate
	originalValidateCandidate := updateValidateCandidate
	originalReplaceBinary := updateReplaceBinary
	t.Cleanup(func() {
		updateWithLock = originalWithLock
		updateFetchLatestRelease = originalFetchLatestRelease
		updateDownloadFile = originalDownloadFile
		updateVerifyChecksum = originalVerifyChecksum
		updateVerifyGitHubAttestations = originalVerifyGitHubAttestations
		updateExtractCandidate = originalExtractCandidate
		updateValidateCandidate = originalValidateCandidate
		updateReplaceBinary = originalReplaceBinary
	})

	runtimeAssetName := "agent-gate_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	statePath := filepath.Join(t.TempDir(), "update.json")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	candidatePath := filepath.Join(t.TempDir(), "agent-gate")
	if err := os.WriteFile(candidatePath, []byte("candidate"), 0o755); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	updateWithLock = func(_ context.Context, _ string, fn func() error) error {
		return fn()
	}
	updateFetchLatestRelease = func(_ context.Context, _ Options) (release, error) {
		return release{
			HTMLURL: "https://example.invalid/release",
			TagName: "202606250140-71-dbb89ef",
			Assets: []releaseAsset{
				{
					Name:               runtimeAssetName,
					BrowserDownloadURL: "https://example.invalid/archive",
					Digest:             "sha256:deadbeef",
				},
			},
		}, nil
	}
	updateDownloadFile = func(_ context.Context, _ *http.Client, _ string, path string) error {
		return os.WriteFile(path, []byte("archive"), 0o600)
	}
	updateVerifyChecksum = func(_ context.Context, _ Options, _ release, _ releaseAsset, _ string) error {
		return nil
	}
	updateVerifyGitHubAttestations = func(_ context.Context, _ Options, _ release, _ releaseAsset, _ string) error {
		return nil
	}
	updateExtractCandidate = func(_ string, _ string) (string, func(), error) {
		return candidatePath, func() {}, nil
	}
	updateValidateCandidate = func(_ context.Context, _ Config, _ string) error {
		return nil
	}
	updateReplaceBinary = func(_, _ string) error {
		t.Fatal("updateReplaceBinary() should not run during dry-run")
		return nil
	}

	options := Options{
		Config:    testConfig(),
		CacheDir:  cacheDir,
		StatePath: statePath,
		DryRun:    true,
	}

	firstResult, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatalf("Apply() first error: %v", err)
	}
	secondResult, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatalf("Apply() second error: %v", err)
	}

	for i, result := range []ApplyResult{firstResult, secondResult} {
		if !result.UpdateAvailable {
			t.Fatalf("result %d UpdateAvailable = false, want true", i)
		}
		if result.Applied {
			t.Fatalf("result %d Applied = true, want false", i)
		}
		if !result.DryRun {
			t.Fatalf("result %d DryRun = false, want true", i)
		}
		if result.LatestTag != "202606250140-71-dbb89ef" {
			t.Fatalf("result %d LatestTag = %q", i, result.LatestTag)
		}
	}

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if state.LastResult != "dry_run" {
		t.Fatalf("LastResult = %q, want dry_run", state.LastResult)
	}
	if state.LatestTag != "202606250140-71-dbb89ef" {
		t.Fatalf("LatestTag = %q", state.LatestTag)
	}
	if state.LastError != "" {
		t.Fatalf("LastError = %q, want empty", state.LastError)
	}
}

func TestApplyCurrentIsIdempotent(t *testing.T) {
	originalWithLock := updateWithLock
	originalFetchLatestRelease := updateFetchLatestRelease
	originalDownloadFile := updateDownloadFile
	t.Cleanup(func() {
		updateWithLock = originalWithLock
		updateFetchLatestRelease = originalFetchLatestRelease
		updateDownloadFile = originalDownloadFile
	})

	runtimeAssetName := "agent-gate_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	statePath := filepath.Join(t.TempDir(), "update.json")
	downloadCallCount := 0

	updateWithLock = func(_ context.Context, _ string, fn func() error) error {
		return fn()
	}
	updateFetchLatestRelease = func(_ context.Context, _ Options) (release, error) {
		return release{
			HTMLURL: "https://example.invalid/release",
			TagName: testCurrentVersion,
			Assets: []releaseAsset{
				{
					Name:               runtimeAssetName,
					BrowserDownloadURL: "https://example.invalid/archive",
					Digest:             "sha256:deadbeef",
				},
			},
		}, nil
	}
	updateDownloadFile = func(_ context.Context, _ *http.Client, _, _ string) error {
		downloadCallCount++
		return nil
	}

	options := Options{
		Config:    testConfig(),
		StatePath: statePath,
	}

	firstResult, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatalf("Apply() first error: %v", err)
	}
	secondResult, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatalf("Apply() second error: %v", err)
	}

	for i, result := range []ApplyResult{firstResult, secondResult} {
		if result.UpdateAvailable {
			t.Fatalf("result %d UpdateAvailable = true, want false", i)
		}
		if result.Applied {
			t.Fatalf("result %d Applied = true, want false", i)
		}
	}
	if downloadCallCount != 0 {
		t.Fatalf("downloadCallCount = %d, want 0", downloadCallCount)
	}

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if state.LastResult != "current" {
		t.Fatalf("LastResult = %q, want current", state.LastResult)
	}
	if state.AppliedTag != "" {
		t.Fatalf("AppliedTag = %q, want empty without prior applied state", state.AppliedTag)
	}
	if state.LastError != "" {
		t.Fatalf("LastError = %q, want empty", state.LastError)
	}
}

func TestApplyCurrentPreservesAppliedTag(t *testing.T) {
	originalWithLock := updateWithLock
	originalFetchLatestRelease := updateFetchLatestRelease
	t.Cleanup(func() {
		updateWithLock = originalWithLock
		updateFetchLatestRelease = originalFetchLatestRelease
	})

	runtimeAssetName := "agent-gate_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	statePath := filepath.Join(t.TempDir(), "update.json")
	if err := SaveState(statePath, State{AppliedTag: "202606250437-78-1cff09c"}); err != nil {
		t.Fatalf("SaveState() setup error: %v", err)
	}

	updateWithLock = func(_ context.Context, _ string, fn func() error) error {
		return fn()
	}
	updateFetchLatestRelease = func(_ context.Context, _ Options) (release, error) {
		return release{
			HTMLURL: "https://example.invalid/release",
			TagName: testCurrentVersion,
			Assets: []releaseAsset{
				{
					Name:               runtimeAssetName,
					BrowserDownloadURL: "https://example.invalid/archive",
					Digest:             "sha256:deadbeef",
				},
			},
		}, nil
	}

	options := Options{
		Config:    testConfig(),
		StatePath: statePath,
	}

	_, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if state.AppliedTag != "202606250437-78-1cff09c" {
		t.Fatalf("AppliedTag = %q", state.AppliedTag)
	}
}

func testSHA256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func assertFileBytes(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error: %v", path, err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("ReadFile(%q) = %q, want %q", path, actual, expected)
	}
}
