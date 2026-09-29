//go:build unix

package selfupdate

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
)

const (
	writeErrorHelperEnv = "SELFUPDATE_DOWNLOAD_WRITE_ERROR_HELPER"
	writeErrorServerEnv = "SELFUPDATE_DOWNLOAD_WRITE_ERROR_SERVER"
	// writeErrorFileLimit is the largest file, in bytes, that the helper
	// process may write. The archive is larger.
	writeErrorFileLimit = 4
)

// TestDownloadWriteErrorHelperProcess runs only inside the child process that
// TestDownloadDoesNotRetryLocalWriteError starts. It caps the file size the
// process may write, ignores the SIGXFSZ signal that the cap raises, and
// prints the VerifyReleaseAssets error.
func TestDownloadWriteErrorHelperProcess(t *testing.T) {
	if os.Getenv(writeErrorHelperEnv) != "1" {
		return
	}
	useShortRetryDelay(t)
	signal.Ignore(syscall.SIGXFSZ)
	limit := syscall.Rlimit{Cur: writeErrorFileLimit, Max: writeErrorFileLimit}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatalf("set file size limit: %v", err)
	}
	serverURL := os.Getenv(writeErrorServerEnv)
	err := VerifyReleaseAssets(context.Background(), Options{
		Config:   Config{Repo: "agoodkind/agent-gate", Binary: "agent-gate", APIBaseURL: serverURL},
		Client:   http.DefaultClient,
		CacheDir: t.TempDir(),
	}, retryTestTag)
	fmt.Printf("verify error: %v\n", err)
}

// TestDownloadDoesNotRetryLocalWriteError downloads an archive in a child
// process that cannot write a file larger than 4 bytes. The temp file write
// fails, and the download must stop after one request.
func TestDownloadDoesNotRetryLocalWriteError(t *testing.T) {
	archive := []byte("release archive larger than the file size limit")
	flaky := newFlakyAssetServer(t, archive, archive, nil)

	helper := exec.Command(os.Args[0], "-test.run=^TestDownloadWriteErrorHelperProcess$", "-test.count=1")
	helper.Env = append(os.Environ(), writeErrorHelperEnv+"=1", writeErrorServerEnv+"="+flaky.server.URL)
	var output bytes.Buffer
	helper.Stdout = &output
	helper.Stderr = &output
	if err := helper.Run(); err != nil {
		t.Fatalf("helper process error: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "verify error: write download temp") {
		t.Fatalf("helper output = %q, want a temp file write error", output.String())
	}
	if got := flaky.downloadRequests(); got != 1 {
		t.Fatalf("download requests = %d, want 1", got)
	}
}
