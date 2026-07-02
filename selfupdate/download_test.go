package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadFileDoesNotUseFixedTempPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("downloaded"))
	}))
	t.Cleanup(server.Close)

	targetPath := filepath.Join(t.TempDir(), "asset.tar.gz")
	fixedTempPath := targetPath + ".tmp"
	if err := os.WriteFile(fixedTempPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	err := downloadFile(context.Background(), server.Client(), server.URL, targetPath)
	if err != nil {
		t.Fatalf("downloadFile() error: %v", err)
	}
	assertFileBytes(t, targetPath, []byte("downloaded"))
	assertFileBytes(t, fixedTempPath, []byte("sentinel"))
}
