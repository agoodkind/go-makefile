package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	testCurrentVersion       = "202606240000-aa-abcdef0"
	engineTestDownloadPrefix = "/agoodkind/agent-gate/releases/download/v1.2.3/"
)

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
	archives := map[string][]byte{
		darwinAssetName:  darwinArchive,
		linuxAssetName:   linuxArchive,
		siblingAssetName: siblingArchive,
	}
	verifiedAttestations := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assetName, found := strings.CutPrefix(request.URL.Path, engineTestDownloadPrefix)
		if !found {
			http.NotFound(writer, request)
			return
		}
		if assetName == checksumsAssetName {
			_, _ = writer.Write([]byte(testChecksums(archives)))
			return
		}
		archive, ok := archives[assetName]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(archive)
	}))
	t.Cleanup(server.Close)

	updateVerifyGitHubAttestations = func(_ context.Context, _ Options, _ release, asset releaseAsset, _ string) error {
		verifiedAttestations = append(verifiedAttestations, asset.Name)
		return nil
	}
	updateExtractCandidate = func(_ string, _ string, _ int64, _ string) (string, func(), error) {
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
			WebBaseURL: server.URL,
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != engineTestDownloadPrefix+checksumsAssetName {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(testChecksums(nil)))
	}))
	t.Cleanup(server.Close)

	options := Options{
		Config: Config{
			Repo:       "agoodkind/agent-gate",
			Binary:     "agent-gate",
			WebBaseURL: server.URL,
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != engineTestDownloadPrefix+checksumsAssetName {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(testChecksums(map[string][]byte{assetName: archive})))
	}))
	t.Cleanup(server.Close)

	options := Options{
		Config: Config{
			Repo:       "agoodkind/agent-gate",
			Binary:     "agent-gate",
			WebBaseURL: server.URL,
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case engineTestDownloadPrefix + checksumsAssetName:
			_, _ = writer.Write([]byte(testChecksums(map[string][]byte{assetName: archive})))
		case engineTestDownloadPrefix + assetName:
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
			WebBaseURL: server.URL,
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

func TestCheckUsesOptionsLoggerForReleaseFeedFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "failure", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	var logOutput strings.Builder
	config := testConfig()
	config.WebBaseURL = server.URL
	options := Options{
		Config:    config,
		Client:    server.Client(),
		StatePath: filepath.Join(t.TempDir(), "update.json"),
		Log:       slog.New(slog.NewTextHandler(&logOutput, nil)),
	}

	_, err := Check(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("Check() error = %v, want HTTP 500", err)
	}
	if !strings.Contains(logOutput.String(), "update release feed query failed") {
		t.Fatalf("options logger output = %q, want release feed query log", logOutput.String())
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
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate})
	installDir := t.TempDir()
	stateDir := t.TempDir()
	writeInstalledBinary(t, filepath.Join(installDir, "alpha"), []byte("old alpha"))
	sizesBefore := directorySizes(t, installDir)

	options := fixture.options(t, "alpha", installDir, stateDir, "version: pure")
	options.DryRun = true

	for i := range 2 {
		result, err := Apply(context.Background(), options)
		if err != nil {
			t.Fatalf("Apply() dry run %d error: %v", i, err)
		}
		if !result.UpdateAvailable {
			t.Fatalf("dry run %d UpdateAvailable = false, want true", i)
		}
		if result.Applied {
			t.Fatalf("dry run %d Applied = true, want false", i)
		}
		if !result.DryRun {
			t.Fatalf("dry run %d DryRun = false, want true", i)
		}
		if result.LatestTag != installDirTestNewTag {
			t.Fatalf("dry run %d LatestTag = %q, want %q", i, result.LatestTag, installDirTestNewTag)
		}
		sizesAfter := directorySizes(t, installDir)
		if !maps.Equal(sizesBefore, sizesAfter) {
			t.Fatalf("install directory changed after dry run %d: before %v, after %v", i, sizesBefore, sizesAfter)
		}
	}

	state, err := LoadState(filepath.Join(stateDir, "update-state.json"))
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if state.LastResult != "dry_run" {
		t.Fatalf("LastResult = %q, want dry_run", state.LastResult)
	}
	if state.LatestTag != installDirTestNewTag {
		t.Fatalf("state LatestTag = %q, want %q", state.LatestTag, installDirTestNewTag)
	}
	if state.LastError != "" {
		t.Fatalf("LastError = %q, want empty", state.LastError)
	}
}

func TestApplyCurrentIsIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	defaultCacheDir := t.TempDir()
	defaultStateDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("XDG_CACHE_HOME", defaultCacheDir)
	t.Setenv("XDG_STATE_HOME", defaultStateDir)

	runtimeAssetName := "agent-gate_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	statePath := filepath.Join(t.TempDir(), "update.json")
	cacheDir := t.TempDir()
	installDir := t.TempDir()
	installPath := filepath.Join(installDir, "agent-gate")
	installedContent := []byte("current agent-gate")
	writeInstalledBinary(t, installPath, installedContent)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agoodkind/agent-gate/releases.atom":
			_, _ = writer.Write([]byte(testReleaseFeed(testCurrentVersion)))
		case "/agoodkind/agent-gate/releases/download/" + testCurrentVersion + "/" + checksumsAssetName:
			_, _ = writer.Write([]byte(testChecksums(map[string][]byte{runtimeAssetName: []byte("archive")})))
		default:
			t.Errorf("unexpected request for current release: %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	config := testConfig()
	config.WebBaseURL = server.URL
	options := Options{
		Config:      config,
		Client:      server.Client(),
		InstallPath: installPath,
		CacheDir:    cacheDir,
		StatePath:   statePath,
	}

	for i := range 2 {
		result, err := Apply(context.Background(), options)
		if err != nil {
			t.Fatalf("Apply() %d error: %v", i, err)
		}
		if result.UpdateAvailable {
			t.Fatalf("result %d UpdateAvailable = true, want false", i)
		}
		if result.Applied {
			t.Fatalf("result %d Applied = true, want false", i)
		}
		assertFileBytes(t, installPath, installedContent)
		assertDirectoryEntries(t, installDir, []string{"agent-gate"})
		for _, directory := range []string{cacheDir, defaultCacheDir, defaultStateDir, homeDir} {
			assertDirectoryEntries(t, directory, nil)
		}
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
