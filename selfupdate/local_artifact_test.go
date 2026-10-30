package selfupdate_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goodkind.io/go-makefile/selfupdate"
)

// The test downloads a real public configsctl release asset and verifies it
// against the live GitHub release, attestation, and Sigstore services.
const (
	acceptanceRepository     = "agoodkind/configsctl"
	acceptanceReleaseTag     = "202610300043-29-7976a64"
	acceptanceAssetName      = "configsctl_linux_amd64.tar.gz"
	acceptanceSHA256         = "032e903daaca614d32fa354965eafa5a13d78ad040cac88ba90f91e1b127dfc9"
	acceptanceSourceRevision = "7976a64a4c3944c6e179daa6e0f6607a3328328b"
	acceptanceSourceRef      = "refs/heads/main"
	acceptanceSignerWorkflow = "https://github.com/agoodkind/go-makefile/.github/workflows/_package.yml@refs/heads/main"
	acceptanceDownloadURL    = "https://github.com/" + acceptanceRepository + "/releases/download/" + acceptanceReleaseTag + "/" + acceptanceAssetName
	acceptanceReadOffset     = 17
	acceptanceRequestTimeout = 2 * time.Minute
)

func TestVerifyLocalArtifactRealRelease(t *testing.T) {
	ctx := context.Background()
	archivePath := downloadAcceptanceArchive(ctx, t)
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = archive.Close() }()
	if _, err := archive.Seek(acceptanceReadOffset, io.SeekStart); err != nil {
		t.Fatalf("seek archive: %v", err)
	}

	result, err := selfupdate.VerifyLocalArtifact(ctx, acceptanceOptions(t, acceptanceSourceRef), archive)
	if err != nil {
		t.Fatalf("VerifyLocalArtifact() error: %v", err)
	}
	want := selfupdate.ArtifactVerificationResult{
		Repository:        acceptanceRepository,
		SignerWorkflowURI: acceptanceSignerWorkflow,
		SourceRevision:    acceptanceSourceRevision,
		SourceRef:         acceptanceSourceRef,
		ReleaseTag:        acceptanceReleaseTag,
		AssetName:         acceptanceAssetName,
		SHA256:            acceptanceSHA256,
	}
	if result != want {
		t.Fatalf("VerifyLocalArtifact() = %+v, want %+v", result, want)
	}
	requireArchiveUnchanged(t, archive)

	_, err = selfupdate.VerifyLocalArtifact(ctx, acceptanceOptions(t, "refs/heads/other"), archive)
	if err == nil {
		t.Fatal("VerifyLocalArtifact() with another source ref error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "SourceRepositoryRef") {
		t.Fatalf("VerifyLocalArtifact() with another source ref error = %v, want SourceRepositoryRef mismatch", err)
	}
	requireArchiveUnchanged(t, archive)
}

func acceptanceOptions(t *testing.T, sourceRef string) selfupdate.ArtifactVerificationOptions {
	t.Helper()
	return selfupdate.ArtifactVerificationOptions{
		Options: selfupdate.Options{
			Config: selfupdate.Config{
				Repo:              acceptanceRepository,
				SignerWorkflowURI: acceptanceSignerWorkflow,
				AuthToken:         os.Getenv("GITHUB_TOKEN"),
			},
			CacheDir: t.TempDir(),
		},
		ReleaseTag:     acceptanceReleaseTag,
		AssetName:      acceptanceAssetName,
		SHA256:         acceptanceSHA256,
		SourceRevision: acceptanceSourceRevision,
		SourceRef:      sourceRef,
	}
}

// requireArchiveUnchanged checks that archive is still open at the test
// offset and that its bytes still match the release digest.
func requireArchiveUnchanged(t *testing.T, archive *os.File) {
	t.Helper()
	offset, err := archive.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("read archive offset: %v", err)
	}
	if offset != acceptanceReadOffset {
		t.Fatalf("archive offset = %d, want %d", offset, acceptanceReadOffset)
	}
	content, err := io.ReadAll(io.NewSectionReader(archive, 0, 1<<40))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != acceptanceSHA256 {
		t.Fatalf("archive SHA-256 = %x, want %s", digest, acceptanceSHA256)
	}
}

func downloadAcceptanceArchive(ctx context.Context, t *testing.T) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, acceptanceDownloadURL, nil)
	if err != nil {
		t.Fatalf("build download request: %v", err)
	}
	client := &http.Client{Timeout: acceptanceRequestTimeout}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("download %s: %v", acceptanceDownloadURL, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download %s: HTTP %d", acceptanceDownloadURL, response.StatusCode)
	}
	var content bytes.Buffer
	if _, err := io.Copy(&content, response.Body); err != nil {
		t.Fatalf("read download: %v", err)
	}
	archivePath := filepath.Join(t.TempDir(), acceptanceAssetName)
	if err := os.WriteFile(archivePath, content.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return archivePath
}
