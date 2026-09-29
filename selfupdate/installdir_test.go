package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	installDirTestRepo       = "agoodkind/selfupdate-probe"
	installDirTestOldVersion = "202601010000-1-0000000"
	installDirTestNewTag     = "202609290000-2-1234567"

	// probeLibrarySource defines the one symbol the cgo candidate imports.
	probeLibrarySource = "int probe_value(void) { return 7; }\n"

	// cgoProbeSource prints a version line only after it calls into the shared
	// library, and the dynamic loader must resolve that library before main runs.
	cgoProbeSource = `package main

/*
int probe_value(void);
*/
import "C"

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("version: probe %d\n", int(C.probe_value()))
		return
	}
	os.Exit(2)
}
`

	pureGoProbeSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("version: pure")
		return
	}
	os.Exit(2)
}
`

	failingProbeSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("candidate refuses to start")
	os.Exit(1)
}
`
)

// releaseFixture serves a release list and its archives from a local server.
type releaseFixture struct {
	server   *httptest.Server
	archives map[string][]byte
}

func newReleaseFixture(t *testing.T, binaries map[string][]byte) *releaseFixture {
	t.Helper()
	fixture := &releaseFixture{archives: map[string][]byte{}}
	for binary, content := range binaries {
		assetName := binary + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
		fixture.archives[assetName] = tarGzipSingleFile(t, binary, content)
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if archiveName, found := strings.CutPrefix(request.URL.Path, "/downloads/"); found {
			archive, ok := fixture.archives[archiveName]
			if !ok {
				http.NotFound(writer, request)
				return
			}
			_, _ = writer.Write(archive)
			return
		}
		if request.URL.Path != "/repos/"+installDirTestRepo+"/releases" {
			http.NotFound(writer, request)
			return
		}
		assets := make([]releaseAsset, 0, len(fixture.archives))
		for assetName, archive := range fixture.archives {
			assets = append(assets, releaseAsset{
				Name:               assetName,
				BrowserDownloadURL: fixture.server.URL + "/downloads/" + assetName,
				Digest:             "sha256:" + testSHA256Hex(archive),
			})
		}
		response := []release{{TagName: installDirTestNewTag, Prerelease: true, Assets: assets}}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("encode release list: %v", err)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *releaseFixture) options(t *testing.T, binary string, installDir string, stateDir string, validateMatch string) Options {
	t.Helper()
	return Options{
		Config: Config{
			Repo:             installDirTestRepo,
			Binary:           binary,
			CurrentVersion:   installDirTestOldVersion,
			CurrentCommit:    "0000000",
			CurrentBuildHash: "buildhash",
			APIBaseURL:       fixture.server.URL,
			ValidateArgs:     []string{"version"},
			ValidateMatch:    validateMatch,
		},
		Client:      fixture.server.Client(),
		InstallPath: filepath.Join(installDir, binary),
		CacheDir:    filepath.Join(stateDir, "cache"),
		StatePath:   filepath.Join(stateDir, "update-state.json"),
	}
}

// skipAttestationVerification replaces the GitHub attestation check, which
// needs a release published by GitHub Actions. Every other step runs for real.
func skipAttestationVerification(t *testing.T) {
	t.Helper()
	original := updateVerifyGitHubAttestations
	t.Cleanup(func() { updateVerifyGitHubAttestations = original })
	updateVerifyGitHubAttestations = func(_ context.Context, _ Options, _ release, _ releaseAsset, _ string) error {
		return nil
	}
}

// TestApplyValidatesOriginRelativeCandidateInInstallDirectory builds a cgo
// binary that loads a shared library through an @loader_path or $ORIGIN
// runpath. The library exists only in the install directory. Apply must
// validate the candidate there and install it.
func TestApplyValidatesOriginRelativeCandidateInInstallDirectory(t *testing.T) {
	skipAttestationVerification(t)
	installDir := t.TempDir()
	libraryName := buildProbeLibrary(t, installDir)
	candidate := buildProbeBinary(t, cgoProbeSource, cgoOriginEnvironment(t, installDir))
	fixture := newReleaseFixture(t, map[string][]byte{"probe": candidate})
	installedPath := filepath.Join(installDir, "probe")
	writeInstalledBinary(t, installedPath, []byte("old probe"))

	result, err := Apply(context.Background(), fixture.options(t, "probe", installDir, t.TempDir(), "version: probe 7"))
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if !result.Applied || result.LatestTag != installDirTestNewTag {
		t.Fatalf("Apply() Applied=%t LatestTag=%q, want true and %q", result.Applied, result.LatestTag, installDirTestNewTag)
	}
	assertFileBytes(t, installedPath, candidate)
	assertDirectoryEntries(t, installDir, []string{libraryName, "probe"})
	output, err := exec.Command(installedPath, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "version: probe 7") {
		t.Fatalf("installed probe version = %q, err %v", output, err)
	}
}

// TestApplyAllLeavesEveryBinaryUnchangedWhenOneCandidateFails stages three
// pure Go candidates. The last one exits with an error during validation.
// ApplyAll must leave all three installed binaries and no staged file behind.
// The second case installs only the two valid candidates and replaces both.
func TestApplyAllLeavesEveryBinaryUnchangedWhenOneCandidateFails(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	failingCandidate := buildProbeBinary(t, failingProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{
		"alpha": pureCandidate,
		"beta":  pureCandidate,
		"gamma": failingCandidate,
	})

	installDir := t.TempDir()
	stateDir := t.TempDir()
	oldContents := map[string][]byte{
		"alpha": []byte("old alpha"),
		"beta":  []byte("old beta"),
		"gamma": []byte("old gamma"),
	}
	for binary, content := range oldContents {
		writeInstalledBinary(t, filepath.Join(installDir, binary), content)
	}
	allOptions := []Options{
		fixture.options(t, "alpha", installDir, stateDir, "version: pure"),
		fixture.options(t, "beta", installDir, stateDir, "version: pure"),
		fixture.options(t, "gamma", installDir, stateDir, "version: pure"),
	}

	_, err := ApplyAll(context.Background(), allOptions)
	if err == nil || !strings.Contains(err.Error(), "candidate version failed") {
		t.Fatalf("ApplyAll() error = %v, want gamma candidate failure", err)
	}
	for binary, content := range oldContents {
		assertFileBytes(t, filepath.Join(installDir, binary), content)
	}
	assertDirectoryEntries(t, installDir, []string{"alpha", "beta", "gamma"})
	state, err := LoadState(filepath.Join(stateDir, "update-state.json"))
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if state.LastResult != "error" {
		t.Fatalf("LastResult = %q, want error", state.LastResult)
	}

	results, err := ApplyAll(context.Background(), allOptions[:2])
	if err != nil {
		t.Fatalf("ApplyAll() valid pair error: %v", err)
	}
	for index, result := range results {
		if !result.Applied {
			t.Fatalf("result %d Applied = false, want true", index)
		}
	}
	assertFileBytes(t, filepath.Join(installDir, "alpha"), pureCandidate)
	assertFileBytes(t, filepath.Join(installDir, "beta"), pureCandidate)
	assertFileBytes(t, filepath.Join(installDir, "gamma"), oldContents["gamma"])
	assertDirectoryEntries(t, installDir, []string{"alpha", "beta", "gamma"})
}

// TestApplyAllRestoresBinariesAfterInterruptedInstall lays out the files that a
// kill during installCandidates leaves: alpha already replaced with its backup
// beside it, beta backed up but not replaced, and a staged beta candidate. The
// next ApplyAll, with no newer release, must restore both backups and remove
// every hidden file.
func TestApplyAllRestoresBinariesAfterInterruptedInstall(t *testing.T) {
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": []byte("unused"), "beta": []byte("unused")})
	installDir := t.TempDir()
	stateDir := t.TempDir()
	files := map[string][]byte{
		"alpha":                   []byte("new alpha from the interrupted install"),
		".alpha-previous-1111":    []byte("old alpha"),
		"beta":                    []byte("old beta"),
		".beta-previous-2222":     []byte("old beta"),
		".beta-candidate-3333":    []byte("staged beta candidate"),
		".gamma-candidate-4444":   []byte("candidate of a binary outside the set"),
		"unrelated-previous-5555": []byte("not a hidden update file"),
	}
	for name, content := range files {
		writeInstalledBinary(t, filepath.Join(installDir, name), content)
	}
	allOptions := []Options{
		fixture.options(t, "alpha", installDir, stateDir, "version: pure"),
		fixture.options(t, "beta", installDir, stateDir, "version: pure"),
	}
	for index := range allOptions {
		allOptions[index].Config.CurrentVersion = installDirTestNewTag
	}

	results, err := ApplyAll(context.Background(), allOptions)
	if err != nil {
		t.Fatalf("ApplyAll() error: %v", err)
	}
	for index, result := range results {
		if result.Applied || result.UpdateAvailable {
			t.Fatalf("result %d Applied=%t UpdateAvailable=%t, want false and false", index, result.Applied, result.UpdateAvailable)
		}
	}
	assertFileBytes(t, filepath.Join(installDir, "alpha"), []byte("old alpha"))
	assertFileBytes(t, filepath.Join(installDir, "beta"), []byte("old beta"))
	assertDirectoryEntries(t, installDir, []string{"alpha", "beta", ".gamma-candidate-4444", "unrelated-previous-5555"})
}

func buildProbeLibrary(t *testing.T, directory string) string {
	t.Helper()
	sourcePath := filepath.Join(t.TempDir(), "probe.c")
	if err := os.WriteFile(sourcePath, []byte(probeLibrarySource), 0o600); err != nil {
		t.Fatalf("write probe library source: %v", err)
	}
	libraryName := "libprobe.so"
	arguments := []string{"-shared", "-fPIC", "-o", filepath.Join(directory, libraryName), sourcePath}
	if runtime.GOOS == "darwin" {
		libraryName = "libprobe.dylib"
		arguments = []string{
			"-dynamiclib",
			"-install_name", "@rpath/" + libraryName,
			"-o", filepath.Join(directory, libraryName),
			sourcePath,
		}
	}
	output, err := exec.Command("cc", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("build probe library: %v\n%s", err, output)
	}
	return libraryName
}

// cgoOriginEnvironment links the probe against the library in libraryDir and
// records only an origin-relative runpath in the binary.
func cgoOriginEnvironment(t *testing.T, libraryDir string) []string {
	t.Helper()
	originRunpath := "$ORIGIN"
	if runtime.GOOS == "darwin" {
		originRunpath = "@loader_path"
	}
	return []string{
		"CGO_ENABLED=1",
		"CGO_LDFLAGS=-L" + libraryDir + " -lprobe -Wl,-rpath," + originRunpath,
	}
}

func buildProbeBinary(t *testing.T, source string, environment []string) []byte {
	t.Helper()
	moduleDir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module probe\n\ngo 1.22\n",
		"main.go": source,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(moduleDir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write probe %s: %v", name, err)
		}
	}
	outputPath := filepath.Join(t.TempDir(), "probe")
	command := exec.Command("go", "build", "-o", outputPath, ".")
	command.Dir = moduleDir
	command.Env = append(os.Environ(), append([]string{"GOWORK=off", "GOFLAGS="}, environment...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build probe binary: %v\n%s", err, output)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read probe binary: %v", err)
	}
	return content
}

func tarGzipSingleFile(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

func writeInstalledBinary(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("write installed binary %s: %v", path, err)
	}
}

// assertDirectoryEntries fails when a staged candidate or a backup file is
// left in the install directory.
func assertDirectoryEntries(t *testing.T, directory string, want []string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	sortedWant := slices.Clone(want)
	slices.Sort(sortedWant)
	if !slices.Equal(names, sortedWant) {
		t.Fatalf("%s entries = %v, want %v", directory, names, sortedWant)
	}
}
