package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"maps"
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

const (
	interruptedApplyHelperEnv  = "SELFUPDATE_INTERRUPTED_APPLY_HELPER"
	interruptedApplyServerEnv  = "SELFUPDATE_INTERRUPTED_APPLY_SERVER"
	interruptedApplyInstallEnv = "SELFUPDATE_INTERRUPTED_APPLY_INSTALL_DIR"
	interruptedApplyStateEnv   = "SELFUPDATE_INTERRUPTED_APPLY_STATE_DIR"
	interruptedApplyPointEnv   = "SELFUPDATE_INTERRUPTED_APPLY_POINT"
	interruptedApplyExitCode   = 3

	// killDuringRenames exits during the second rename, after alpha is
	// replaced and before beta is replaced.
	killDuringRenames = "renames"
	// killDuringCleanup exits during the first backup removal, after both
	// renames and the commit marker.
	killDuringCleanup = "cleanup"
)

// TestInterruptedApplyHelperProcess runs only inside the child process that
// runInterruptedApply starts. It applies the alpha and beta set and exits the
// process at the point that SELFUPDATE_INTERRUPTED_APPLY_POINT selects.
func TestInterruptedApplyHelperProcess(t *testing.T) {
	if os.Getenv(interruptedApplyHelperEnv) != "1" {
		return
	}
	skipAttestationVerification(t)
	switch os.Getenv(interruptedApplyPointEnv) {
	case killDuringRenames:
		renameCount := 0
		updateInstallCandidate = func(candidatePath string, installPath string) error {
			renameCount++
			if renameCount == 2 {
				os.Exit(interruptedApplyExitCode)
			}
			return installCandidate(candidatePath, installPath)
		}
	case killDuringCleanup:
		updateRemoveBackup = func(_ string) error {
			os.Exit(interruptedApplyExitCode)
			return nil
		}
	default:
		t.Fatalf("unknown kill point %q", os.Getenv(interruptedApplyPointEnv))
	}
	fixture := &releaseFixture{server: &httptest.Server{URL: os.Getenv(interruptedApplyServerEnv)}}
	installDir := os.Getenv(interruptedApplyInstallEnv)
	stateDir := os.Getenv(interruptedApplyStateEnv)
	allOptions := []Options{
		fixture.options(t, "alpha", installDir, stateDir, "version: pure"),
		fixture.options(t, "beta", installDir, stateDir, "version: pure"),
	}
	for index := range allOptions {
		allOptions[index].Client = http.DefaultClient
	}
	_, err := ApplyAll(context.Background(), allOptions)
	t.Fatalf("ApplyAll() returned before the kill point: %v", err)
}

// interruptedApply is one alpha and beta install directory with a child
// ApplyAll that a test kills at a chosen point.
type interruptedApply struct {
	fixture    *releaseFixture
	candidate  []byte
	installDir string
	stateDir   string
}

func runInterruptedApply(t *testing.T, killPoint string) interruptedApply {
	t.Helper()
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	run := interruptedApply{
		fixture:    newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate}),
		candidate:  pureCandidate,
		installDir: t.TempDir(),
		stateDir:   t.TempDir(),
	}
	writeInstalledBinary(t, filepath.Join(run.installDir, "alpha"), []byte("old alpha"))
	writeInstalledBinary(t, filepath.Join(run.installDir, "beta"), []byte("old beta"))

	helper := exec.Command(os.Args[0], "-test.run=^TestInterruptedApplyHelperProcess$", "-test.count=1")
	helper.Env = append(os.Environ(),
		interruptedApplyHelperEnv+"=1",
		interruptedApplyPointEnv+"="+killPoint,
		interruptedApplyServerEnv+"="+run.fixture.server.URL,
		interruptedApplyInstallEnv+"="+run.installDir,
		interruptedApplyStateEnv+"="+run.stateDir,
	)
	output, err := helper.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != interruptedApplyExitCode {
		t.Fatalf("helper process error = %v, want exit code %d\n%s", err, interruptedApplyExitCode, output)
	}
	return run
}

// applyWithoutNewRelease runs ApplyAll against the same fixture with a current
// version equal to the latest tag, so only the leftover recovery acts.
func (run interruptedApply) applyWithoutNewRelease(t *testing.T) {
	t.Helper()
	allOptions := []Options{
		run.fixture.options(t, "alpha", run.installDir, run.stateDir, "version: pure"),
		run.fixture.options(t, "beta", run.installDir, run.stateDir, "version: pure"),
	}
	for index := range allOptions {
		allOptions[index].Config.CurrentVersion = installDirTestNewTag
	}
	results, err := ApplyAll(context.Background(), allOptions)
	if err != nil {
		t.Fatalf("ApplyAll() after the kill error: %v", err)
	}
	for index, result := range results {
		if result.Applied || result.UpdateAvailable {
			t.Fatalf("result %d Applied=%t UpdateAvailable=%t, want false and false", index, result.Applied, result.UpdateAvailable)
		}
	}
	if _, err := os.Stat(filepath.Join(run.stateDir, "update-install-committed")); !os.IsNotExist(err) {
		t.Fatalf("commit marker still present after recovery: %v", err)
	}
}

func assertHiddenPrefixes(t *testing.T, directory string, want []string) {
	t.Helper()
	leftovers := hiddenEntryPrefixes(t, directory)
	for _, prefix := range want {
		if !slices.Contains(leftovers, prefix) {
			t.Fatalf("killed apply left %v, want an entry starting with %s", leftovers, prefix)
		}
	}
}

// TestApplyAllRestoresBinariesAfterKilledApply kills a real ApplyAll between
// its two renames. The next ApplyAll with no newer release must restore the
// old alpha from its backup, keep the old beta, and remove every leftover file.
func TestApplyAllRestoresBinariesAfterKilledApply(t *testing.T) {
	run := runInterruptedApply(t, killDuringRenames)
	assertFileBytes(t, filepath.Join(run.installDir, "alpha"), run.candidate)
	assertFileBytes(t, filepath.Join(run.installDir, "beta"), []byte("old beta"))
	assertHiddenPrefixes(t, run.installDir, []string{".alpha-previous-", ".beta-previous-", ".beta-candidate-"})

	run.applyWithoutNewRelease(t)
	assertFileBytes(t, filepath.Join(run.installDir, "alpha"), []byte("old alpha"))
	assertFileBytes(t, filepath.Join(run.installDir, "beta"), []byte("old beta"))
	assertDirectoryEntries(t, run.installDir, []string{"alpha", "beta"})
}

// TestApplyAllKeepsNewBinariesAfterKillDuringCleanup kills a real ApplyAll
// during backup removal, after both renames and the commit marker. The next
// ApplyAll with no newer release must keep both new binaries and remove every
// leftover backup and the marker.
func TestApplyAllKeepsNewBinariesAfterKillDuringCleanup(t *testing.T) {
	run := runInterruptedApply(t, killDuringCleanup)
	assertFileBytes(t, filepath.Join(run.installDir, "alpha"), run.candidate)
	assertFileBytes(t, filepath.Join(run.installDir, "beta"), run.candidate)
	assertHiddenPrefixes(t, run.installDir, []string{".alpha-previous-", ".beta-previous-"})

	run.applyWithoutNewRelease(t)
	assertFileBytes(t, filepath.Join(run.installDir, "alpha"), run.candidate)
	assertFileBytes(t, filepath.Join(run.installDir, "beta"), run.candidate)
	assertDirectoryEntries(t, run.installDir, []string{"alpha", "beta"})
}

// TestApplyAllDryRunLeavesInstallDirectoryUnchanged runs a dry run with a newer
// release available and a read-only install directory. The dry run must
// succeed, the install directory names and sizes must not change, and every
// result must report the skipped launch check.
func TestApplyAllDryRunLeavesInstallDirectoryUnchanged(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate})
	installDir := t.TempDir()
	stateDir := t.TempDir()
	writeInstalledBinary(t, filepath.Join(installDir, "alpha"), []byte("old alpha"))
	writeInstalledBinary(t, filepath.Join(installDir, "beta"), []byte("old beta"))
	before := directorySizes(t, installDir)
	// A read-only install directory makes any write during the dry run fail.
	if err := os.Chmod(installDir, 0o555); err != nil {
		t.Fatalf("make install directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(installDir, 0o755) })

	allOptions := []Options{
		fixture.options(t, "alpha", installDir, stateDir, "version: pure"),
		fixture.options(t, "beta", installDir, stateDir, "version: pure"),
	}
	for index := range allOptions {
		allOptions[index].DryRun = true
	}
	results, err := ApplyAll(context.Background(), allOptions)
	if err != nil {
		t.Fatalf("ApplyAll() dry run error: %v", err)
	}
	for index, result := range results {
		if !result.UpdateAvailable || result.Applied || !result.DryRun || !result.LaunchCheckSkipped {
			t.Fatalf("result %d UpdateAvailable=%t Applied=%t DryRun=%t LaunchCheckSkipped=%t, want true false true true",
				index, result.UpdateAvailable, result.Applied, result.DryRun, result.LaunchCheckSkipped)
		}
	}
	after := directorySizes(t, installDir)
	if !maps.Equal(before, after) {
		t.Fatalf("install directory changed during a dry run: before %v, after %v", before, after)
	}
	state, err := LoadState(filepath.Join(stateDir, "update-state.json"))
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if state.LastResult != "dry_run" {
		t.Fatalf("LastResult = %q, want dry_run", state.LastResult)
	}
}

// hiddenEntryPrefixes returns each hidden entry name up to and including its
// last dash, which strips the random suffix os.CreateTemp adds.
func hiddenEntryPrefixes(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	prefixes := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".") {
			continue
		}
		prefixes = append(prefixes, name[:strings.LastIndex(name, "-")+1])
	}
	return prefixes
}

func directorySizes(t *testing.T, directory string) map[string]int64 {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	sizes := make(map[string]int64, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", entry.Name(), err)
		}
		sizes[entry.Name()] = info.Size()
	}
	return sizes
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
