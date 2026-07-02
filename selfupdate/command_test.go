package selfupdate

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunUpdateCommandCheckPrintsFacts(t *testing.T) {
	restoreCommandTestSeams(t, "202606250140-71-dbb89ef")

	statePath := filepath.Join(t.TempDir(), "update.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := RunUpdateCommand(
		context.Background(),
		Options{Config: testConfig(), StatePath: statePath},
		[]string{"--check"},
		&stdout,
		&stderr,
	)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0, stderr %q", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"current: " + testCurrentVersion,
		"latest: 202606250140-71-dbb89ef",
		"available: true",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, want substring %q", output, want)
		}
	}
}

func TestRunUpdateCommandDryRunPrintsApplyFacts(t *testing.T) {
	restoreCommandTestSeams(t, "202606250140-71-dbb89ef")

	tempDir := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := RunUpdateCommand(
		context.Background(),
		Options{
			Config:    testConfig(),
			CacheDir:  filepath.Join(tempDir, "cache"),
			StatePath: filepath.Join(tempDir, "update.json"),
		},
		[]string{"--dry-run"},
		&stdout,
		&stderr,
	)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0, stderr %q", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"available: true",
		"applied: false",
		"dry_run: true",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, want substring %q", output, want)
		}
	}
}

func TestRunUpdateCommandRejectsUnexpectedArgs(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := RunUpdateCommand(
		context.Background(),
		Options{Config: testConfig()},
		[]string{"extra"},
		&stdout,
		&stderr,
	)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Fatalf("stderr = %q, want unexpected argument", stderr.String())
	}
}

func restoreCommandTestSeams(t *testing.T, latestTag string) {
	t.Helper()
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
			TagName: latestTag,
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
		return nil
	}
}
