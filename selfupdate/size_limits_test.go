package selfupdate

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExtractCandidateHonorsCallerLimit proves the unpacked-binary ceiling comes
// from the caller rather than from a package constant. The same archive must be
// rejected under a limit below its size and accepted under one above it, which
// is what lets a consumer whose binary is larger than the default install at
// all.
func TestExtractCandidateHonorsCallerLimit(t *testing.T) {
	skipAttestationVerification(t)
	candidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": candidate})
	installDir := t.TempDir()
	installedPath := filepath.Join(installDir, "alpha")
	writeInstalledBinary(t, installedPath, []byte("old alpha"))

	rejecting := fixture.options(t, "alpha", installDir, t.TempDir(), "version: pure")
	rejecting.Config.MaxBinaryBytes = int64(len(candidate)) - 1
	_, err := Apply(context.Background(), rejecting)
	if err == nil {
		t.Fatal("Apply() error = nil, want a binary size rejection below the limit")
	}
	if !strings.Contains(err.Error(), "outside allowed range") {
		t.Fatalf("Apply() error = %v, want a binary size rejection", err)
	}
	assertFileBytes(t, installedPath, []byte("old alpha"))

	accepting := fixture.options(t, "alpha", installDir, t.TempDir(), "version: pure")
	accepting.Config.MaxBinaryBytes = int64(len(candidate))
	if _, err := Apply(context.Background(), accepting); err != nil {
		t.Fatalf("Apply() error under a sufficient limit: %v", err)
	}
	assertFileBytes(t, installedPath, candidate)
}

// TestDownloadFileHonorsCallerLimit proves the download ceiling is the caller's
// too, so raising the unpacked limit alone would not be enough for a consumer
// whose compressed asset is also large.
func TestDownloadFileHonorsCallerLimit(t *testing.T) {
	skipAttestationVerification(t)
	candidate := buildProbeBinary(t, pureGoProbeSource, []string{"CGO_ENABLED=0"})
	fixture := newReleaseFixture(t, map[string][]byte{"alpha": candidate})
	archiveName := "alpha_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	archiveSize := int64(len(fixture.archives[archiveName]))
	installDir := t.TempDir()
	installedPath := filepath.Join(installDir, "alpha")
	writeInstalledBinary(t, installedPath, []byte("old alpha"))

	rejecting := fixture.options(t, "alpha", installDir, t.TempDir(), "version: pure")
	rejecting.Config.MaxDownloadBytes = archiveSize - 1
	_, err := Apply(context.Background(), rejecting)
	if err == nil {
		t.Fatal("Apply() error = nil, want a download size rejection below the limit")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Apply() error = %v, want a download size rejection", err)
	}
	assertFileBytes(t, installedPath, []byte("old alpha"))

	accepting := fixture.options(t, "alpha", installDir, t.TempDir(), "version: pure")
	accepting.Config.MaxDownloadBytes = archiveSize
	if _, err := Apply(context.Background(), accepting); err != nil {
		t.Fatalf("Apply() error under a sufficient limit: %v", err)
	}
	assertFileBytes(t, installedPath, candidate)
}
