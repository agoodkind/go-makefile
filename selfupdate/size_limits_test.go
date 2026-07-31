package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCandidateArchive builds a gzip tar holding one member named binary with
// the given byte length, which is what extractCandidate reads and size-checks.
func writeCandidateArchive(t *testing.T, path string, binary string, size int) {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{Name: binary, Mode: 0o755, Size: int64(size)}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("WriteHeader() error: %v", err)
	}
	if _, err := tarWriter.Write(bytes.Repeat([]byte("x"), size)); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("tar Close() error: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip Close() error: %v", err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
}

// TestExtractCandidateHonorsCallerLimit proves the unpacked-binary ceiling comes
// from the caller rather than from a package constant. The same archive must be
// rejected under a limit below its size and accepted under one above it, which
// is what lets a consumer whose binary is larger than the default install at
// all.
func TestExtractCandidateHonorsCallerLimit(t *testing.T) {
	const binary = "sized-binary"
	const size = 4096
	archivePath := filepath.Join(t.TempDir(), "asset.tar.gz")
	writeCandidateArchive(t, archivePath, binary, size)

	_, rejectCleanup, err := extractCandidate(archivePath, binary, size-1)
	rejectCleanup()
	if err == nil {
		t.Fatal("extractCandidate() error = nil, want a size rejection below the limit")
	}
	if !strings.Contains(err.Error(), "outside allowed range") {
		t.Fatalf("extractCandidate() error = %v, want a size rejection", err)
	}

	candidatePath, cleanup, err := extractCandidate(archivePath, binary, size)
	t.Cleanup(cleanup)
	if err != nil {
		t.Fatalf("extractCandidate() error: %v", err)
	}
	info, err := os.Stat(candidatePath)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if info.Size() != size {
		t.Fatalf("candidate size = %d, want %d", info.Size(), size)
	}
}

// TestDownloadFileHonorsCallerLimit proves the download ceiling is the caller's
// too, so raising the unpacked limit alone would not be enough for a consumer
// whose compressed asset is also large.
func TestDownloadFileHonorsCallerLimit(t *testing.T) {
	body := bytes.Repeat([]byte("y"), 4096)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(body)
	}))
	t.Cleanup(server.Close)

	targetDir := t.TempDir()
	rejectPath := filepath.Join(targetDir, "reject.tar.gz")
	err := downloadFile(context.Background(), server.Client(), server.URL, rejectPath, int64(len(body))-1)
	if err == nil {
		t.Fatal("downloadFile() error = nil, want a size rejection below the limit")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("downloadFile() error = %v, want a size rejection", err)
	}

	acceptPath := filepath.Join(targetDir, "accept.tar.gz")
	if err := downloadFile(context.Background(), server.Client(), server.URL, acceptPath, int64(len(body))); err != nil {
		t.Fatalf("downloadFile() error: %v", err)
	}
	assertFileBytes(t, acceptPath, body)
}

// TestConfigDefaultsSizeLimits proves an unset limit takes the package default
// rather than zero, since a zero ceiling would reject every binary.
func TestConfigDefaultsSizeLimits(t *testing.T) {
	resolved := Config{}.withDefaults()
	if resolved.MaxDownloadBytes != defaultMaxDownloadBytes {
		t.Fatalf("MaxDownloadBytes = %d, want %d", resolved.MaxDownloadBytes, defaultMaxDownloadBytes)
	}
	if resolved.MaxBinaryBytes != defaultMaxBinaryBytes {
		t.Fatalf("MaxBinaryBytes = %d, want %d", resolved.MaxBinaryBytes, defaultMaxBinaryBytes)
	}

	overridden := Config{MaxDownloadBytes: 1, MaxBinaryBytes: 2}.withDefaults()
	if overridden.MaxDownloadBytes != 1 {
		t.Fatalf("MaxDownloadBytes = %d, want the caller value 1", overridden.MaxDownloadBytes)
	}
	if overridden.MaxBinaryBytes != 2 {
		t.Fatalf("MaxBinaryBytes = %d, want the caller value 2", overridden.MaxBinaryBytes)
	}
}
