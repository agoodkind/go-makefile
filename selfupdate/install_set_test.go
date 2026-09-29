package selfupdate

import (
	"context"
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
