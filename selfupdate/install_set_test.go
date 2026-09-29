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
