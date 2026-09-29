package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installSetOptions returns install options for alpha, beta, and gamma from
// fixture, with installed files that the test writes before the install.
func installSetOptions(t *testing.T, fixture *releaseFixture) (InstallReleaseBinariesOptions, string, map[string][]byte) {
	t.Helper()
	binDir := t.TempDir()
	stateDir := t.TempDir()
	oldContents := map[string][]byte{
		"alpha": []byte("old alpha"),
		"beta":  []byte("old beta"),
		"gamma": []byte("old gamma"),
	}
	for binary, content := range oldContents {
		writeInstalledBinary(t, filepath.Join(binDir, binary), content)
	}
	options := make([]Options, 0, len(oldContents))
	for _, binary := range []string{"alpha", "beta", "gamma"} {
		options = append(options, fixture.options(t, binary, binDir, stateDir, "version: pure"))
	}
	return InstallReleaseBinariesOptions{Options: options, Channel: ReleaseChannelRolling, BinDir: binDir}, binDir, oldContents
}

func assertInstalledSetUnchanged(t *testing.T, binDir string, oldContents map[string][]byte) {
	t.Helper()
	for binary, content := range oldContents {
		assertFileBytes(t, filepath.Join(binDir, binary), content)
	}
	assertDirectoryEntries(t, binDir, []string{"alpha", "beta", "gamma"})
}

// emptyInstallSetOptions returns install options for alpha, beta, and gamma
// from fixture into a new, empty bin directory.
func emptyInstallSetOptions(t *testing.T, fixture *releaseFixture) (InstallReleaseBinariesOptions, string) {
	t.Helper()
	binDir := t.TempDir()
	stateDir := t.TempDir()
	options := make([]Options, 0, 3)
	for _, binary := range []string{"alpha", "beta", "gamma"} {
		options = append(options, fixture.options(t, binary, binDir, stateDir, "version: pure"))
	}
	return InstallReleaseBinariesOptions{Options: options, Channel: ReleaseChannelRolling, BinDir: binDir}, binDir
}

// TestInstallReleaseBinariesInstallsEveryBinaryIntoEmptyDirectory installs
// three valid candidates into an empty bin directory, the first install on a
// new machine.
func TestInstallReleaseBinariesInstallsEveryBinaryIntoEmptyDirectory(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": pureCandidate})
	installOptions, binDir := emptyInstallSetOptions(t, fixture)

	if _, err := InstallReleaseBinaries(context.Background(), installOptions); err != nil {
		t.Fatalf("InstallReleaseBinaries() error: %v", err)
	}
	for _, binary := range []string{"alpha", "beta", "gamma"} {
		assertFileBytes(t, filepath.Join(binDir, binary), pureCandidate)
	}
	assertDirectoryEntries(t, binDir, []string{"alpha", "beta", "gamma"})
}

// TestInstallReleaseBinariesLeavesEmptyDirectoryWhenOneCandidateFails stages
// three candidates into an empty bin directory. The last one exits with an
// error during validation. The directory must stay empty, with no staged
// candidate left behind.
func TestInstallReleaseBinariesLeavesEmptyDirectoryWhenOneCandidateFails(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	failingCandidate := buildProbeBinary(t, failingProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": failingCandidate})
	installOptions, binDir := emptyInstallSetOptions(t, fixture)

	_, err := InstallReleaseBinaries(context.Background(), installOptions)
	if err == nil || !strings.Contains(err.Error(), "candidate version failed") {
		t.Fatalf("InstallReleaseBinaries() error = %v, want gamma candidate failure", err)
	}
	assertDirectoryEntries(t, binDir, []string{})
}

// TestInstallReleaseBinariesRejectsRepeatedBinary passes alpha twice. The
// install must fail before it contacts the release server.
func TestInstallReleaseBinariesRejectsRepeatedBinary(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate})
	binDir := t.TempDir()
	stateDir := t.TempDir()
	installOptions := InstallReleaseBinariesOptions{
		Options: []Options{
			fixture.options(t, "alpha", binDir, stateDir, "version: pure"),
			fixture.options(t, "alpha", binDir, stateDir, "version: pure"),
		},
		Channel: ReleaseChannelRolling,
		BinDir:  binDir,
	}

	_, err := InstallReleaseBinaries(context.Background(), installOptions)
	if err == nil || !strings.Contains(err.Error(), "repeats binary alpha") {
		t.Fatalf("InstallReleaseBinaries() error = %v, want repeated binary error", err)
	}
	assertDirectoryEntries(t, binDir, []string{})
}

// TestInstallReleaseBinariesReplacesEveryBinary installs three valid
// candidates over an existing install. Every binary must be replaced.
func TestInstallReleaseBinariesReplacesEveryBinary(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": pureCandidate})
	installOptions, binDir, _ := installSetOptions(t, fixture)

	results, err := InstallReleaseBinaries(context.Background(), installOptions)
	if err != nil {
		t.Fatalf("InstallReleaseBinaries() error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for _, result := range results {
		if result.Tag != installDirTestNewTag {
			t.Fatalf("result %s Tag = %q, want %q", result.InstallPath, result.Tag, installDirTestNewTag)
		}
		assertFileBytes(t, result.InstallPath, pureCandidate)
	}
	assertDirectoryEntries(t, binDir, []string{"alpha", "beta", "gamma"})
}

// TestInstallReleaseBinariesLeavesInstalledSetWhenOneCandidateFails stages
// three candidates. The last one exits with an error during validation. Every
// installed binary must stay byte-identical.
func TestInstallReleaseBinariesLeavesInstalledSetWhenOneCandidateFails(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	failingCandidate := buildProbeBinary(t, failingProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": failingCandidate})
	installOptions, binDir, oldContents := installSetOptions(t, fixture)

	_, err := InstallReleaseBinaries(context.Background(), installOptions)
	if err == nil || !strings.Contains(err.Error(), "candidate version failed") {
		t.Fatalf("InstallReleaseBinaries() error = %v, want gamma candidate failure", err)
	}
	assertInstalledSetUnchanged(t, binDir, oldContents)
}

// TestInstallReleaseBinariesLeavesInstalledSetWhenOneDownloadFails answers
// the last archive download with HTTP 404. Every installed binary must stay
// byte-identical.
func TestInstallReleaseBinariesLeavesInstalledSetWhenOneDownloadFails(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": pureCandidate})
	fixture.refusedDownloads["gamma_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz"] = true
	installOptions, binDir, oldContents := installSetOptions(t, fixture)

	_, err := InstallReleaseBinaries(context.Background(), installOptions)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("InstallReleaseBinaries() error = %v, want gamma download HTTP 404", err)
	}
	assertInstalledSetUnchanged(t, binDir, oldContents)
}

const (
	setLinkName      = "libprobe.1"
	setOldLinkTarget = "libprobe.old"
	setNewLinkTarget = "libprobe.new"

	// envProbeSource prints a version line only when PROBE_READY=1.
	envProbeSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" && os.Getenv("PROBE_READY") == "1" {
		fmt.Println("version: pure")
		return
	}
	os.Exit(2)
}
`
)

// addInstalledSymlink writes the old and new link targets as files and points
// setLinkName at the old one, as an earlier install would.
func addInstalledSymlink(t *testing.T, installOptions *InstallReleaseBinariesOptions, binDir string) {
	t.Helper()
	writeInstalledBinary(t, filepath.Join(binDir, setOldLinkTarget), []byte("old library"))
	writeInstalledBinary(t, filepath.Join(binDir, setNewLinkTarget), []byte("new library"))
	if err := os.Symlink(setOldLinkTarget, filepath.Join(binDir, setLinkName)); err != nil {
		t.Fatalf("create installed symlink: %v", err)
	}
	installOptions.Symlinks = []InstallSymlink{{Name: setLinkName, Target: setNewLinkTarget}}
}

func assertSymlinkTarget(t *testing.T, path string, want string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s mode = %v, want a symlink", path, info.Mode())
	}
	target, err := os.Readlink(path)
	if err != nil || target != want {
		t.Fatalf("readlink %s = %q, %v, want %q", path, target, err, want)
	}
}

// TestInstallReleaseBinariesCommitsSymlinkWithBinaries installs three valid
// candidates and repoints an installed symlink in the same commit.
func TestInstallReleaseBinariesCommitsSymlinkWithBinaries(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": pureCandidate})
	installOptions, binDir, _ := installSetOptions(t, fixture)
	addInstalledSymlink(t, &installOptions, binDir)

	if _, err := InstallReleaseBinaries(context.Background(), installOptions); err != nil {
		t.Fatalf("InstallReleaseBinaries() error: %v", err)
	}
	assertSymlinkTarget(t, filepath.Join(binDir, setLinkName), setNewLinkTarget)
	assertDirectoryEntries(t, binDir, []string{"alpha", "beta", "gamma", setLinkName, setOldLinkTarget, setNewLinkTarget})
}

// TestInstallReleaseBinariesLeavesSymlinkWhenOneCandidateFails fails gamma
// during validation. The installed symlink must keep its old target.
func TestInstallReleaseBinariesLeavesSymlinkWhenOneCandidateFails(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	failingCandidate := buildProbeBinary(t, failingProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": failingCandidate})
	installOptions, binDir, oldContents := installSetOptions(t, fixture)
	addInstalledSymlink(t, &installOptions, binDir)

	_, err := InstallReleaseBinaries(context.Background(), installOptions)
	if err == nil || !strings.Contains(err.Error(), "candidate version failed") {
		t.Fatalf("InstallReleaseBinaries() error = %v, want gamma candidate failure", err)
	}
	for binary, content := range oldContents {
		assertFileBytes(t, filepath.Join(binDir, binary), content)
	}
	assertSymlinkTarget(t, filepath.Join(binDir, setLinkName), setOldLinkTarget)
	assertDirectoryEntries(t, binDir, []string{"alpha", "beta", "gamma", setLinkName, setOldLinkTarget, setNewLinkTarget})
}

// TestInstallReleaseBinariesRestoresSymlinkWhenCommitFails replaces gamma's
// installed binary with a non-empty directory, which the commit step cannot
// back up. The commit replaces the symlink, alpha, and beta first, and the
// rollback must restore all three, the symlink as a symlink.
func TestInstallReleaseBinariesRestoresSymlinkWhenCommitFails(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": pureCandidate})
	installOptions, binDir, oldContents := installSetOptions(t, fixture)
	addInstalledSymlink(t, &installOptions, binDir)
	gammaPath := filepath.Join(binDir, "gamma")
	if err := os.Remove(gammaPath); err != nil {
		t.Fatalf("remove gamma: %v", err)
	}
	if err := os.Mkdir(gammaPath, 0o755); err != nil {
		t.Fatalf("create gamma directory: %v", err)
	}
	writeInstalledBinary(t, filepath.Join(gammaPath, "occupied"), []byte("directory content"))

	if _, err := InstallReleaseBinaries(context.Background(), installOptions); err == nil {
		t.Fatal("InstallReleaseBinaries() error = nil, want a gamma commit failure")
	}
	assertFileBytes(t, filepath.Join(binDir, "alpha"), oldContents["alpha"])
	assertFileBytes(t, filepath.Join(binDir, "beta"), oldContents["beta"])
	assertSymlinkTarget(t, filepath.Join(binDir, setLinkName), setOldLinkTarget)
	assertDirectoryEntries(t, binDir, []string{"alpha", "beta", "gamma", setLinkName, setOldLinkTarget, setNewLinkTarget})
}

// TestInstallReleaseBinariesPassesValidateEnv installs a candidate that
// prints its version only when PROBE_READY=1 is in its environment.
func TestInstallReleaseBinariesPassesValidateEnv(t *testing.T) {
	skipAttestationVerification(t)
	envCandidate := buildProbeBinary(t, envProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": envCandidate})
	binDir := t.TempDir()
	option := fixture.options(t, "alpha", binDir, t.TempDir(), "version: pure")
	installOptions := InstallReleaseBinariesOptions{Options: []Options{option}, Channel: ReleaseChannelRolling, BinDir: binDir}

	if _, err := InstallReleaseBinaries(context.Background(), installOptions); err == nil {
		t.Fatal("InstallReleaseBinaries() without ValidateEnv error = nil, want a candidate failure")
	}
	installOptions.Options[0].Config.ValidateEnv = []string{"PROBE_READY=1"}
	if _, err := InstallReleaseBinaries(context.Background(), installOptions); err != nil {
		t.Fatalf("InstallReleaseBinaries() with ValidateEnv error: %v", err)
	}
	assertFileBytes(t, filepath.Join(binDir, "alpha"), envCandidate)
}

// TestInstallReleaseBinariesRejectsSymlinkToMissingTarget points the staged
// symlink at a file that does not exist. The install must fail and leave the
// binaries and the installed symlink unchanged.
func TestInstallReleaseBinariesRejectsSymlinkToMissingTarget(t *testing.T) {
	skipAttestationVerification(t)
	pureCandidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": pureCandidate, "beta": pureCandidate, "gamma": pureCandidate})
	installOptions, binDir, oldContents := installSetOptions(t, fixture)
	addInstalledSymlink(t, &installOptions, binDir)
	installOptions.Symlinks = []InstallSymlink{{Name: setLinkName, Target: "libprobe.missing"}}

	_, err := InstallReleaseBinaries(context.Background(), installOptions)
	if err == nil || !strings.Contains(err.Error(), "target libprobe.missing") {
		t.Fatalf("InstallReleaseBinaries() error = %v, want a missing symlink target error", err)
	}
	for binary, content := range oldContents {
		assertFileBytes(t, filepath.Join(binDir, binary), content)
	}
	assertSymlinkTarget(t, filepath.Join(binDir, setLinkName), setOldLinkTarget)
	assertDirectoryEntries(t, binDir, []string{"alpha", "beta", "gamma", setLinkName, setOldLinkTarget, setNewLinkTarget})
}
