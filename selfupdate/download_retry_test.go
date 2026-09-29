package selfupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	retryTestTag       = "v1.2.3"
	retryTestAssetName = "agent-gate_linux_arm64.tar.gz"

	// truncatedBody in a failure list makes the server announce a
	// Content-Length longer than the body it writes. The client read then
	// fails before the announced end.
	truncatedBody          = 0
	truncatedBodyShortfall = 64
)

// flakyAssetServer serves one release. Its archive download answers with the
// statuses in failures, in order, and then serves body with HTTP 200.
type flakyAssetServer struct {
	server   *httptest.Server
	mutex    sync.Mutex
	failures []int
	body     []byte
	requests int
}

func newFlakyAssetServer(t *testing.T, archive []byte, body []byte, failures []int) *flakyAssetServer {
	t.Helper()
	flaky := &flakyAssetServer{failures: failures, body: body}
	flaky.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/agoodkind/agent-gate/releases/tags/" + retryTestTag:
			response := release{
				TagName: retryTestTag,
				Assets: []releaseAsset{{
					Name:               retryTestAssetName,
					BrowserDownloadURL: flaky.server.URL + "/downloads/" + retryTestAssetName,
					Digest:             "sha256:" + testSHA256Hex(archive),
				}},
			}
			if err := json.NewEncoder(writer).Encode(response); err != nil {
				t.Errorf("encode release: %v", err)
			}
		case "/downloads/" + retryTestAssetName:
			flaky.mutex.Lock()
			flaky.requests++
			status := http.StatusOK
			if len(flaky.failures) > 0 {
				status = flaky.failures[0]
				flaky.failures = flaky.failures[1:]
			}
			flaky.mutex.Unlock()
			if status == truncatedBody {
				writer.Header().Set("Content-Length", strconv.Itoa(len(flaky.body)+truncatedBodyShortfall))
				_, _ = writer.Write(flaky.body)
				return
			}
			if status != http.StatusOK {
				http.Error(writer, "injected failure", status)
				return
			}
			_, _ = writer.Write(flaky.body)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(flaky.server.Close)
	return flaky
}

func (flaky *flakyAssetServer) downloadRequests() int {
	flaky.mutex.Lock()
	defer flaky.mutex.Unlock()
	return flaky.requests
}

func (flaky *flakyAssetServer) verify(t *testing.T) (string, error) {
	t.Helper()
	cacheDir := t.TempDir()
	err := VerifyReleaseAssets(context.Background(), Options{
		Config:   Config{Repo: "agoodkind/agent-gate", Binary: "agent-gate", APIBaseURL: flaky.server.URL},
		Client:   flaky.server.Client(),
		CacheDir: cacheDir,
	}, retryTestTag)
	return filepath.Join(cacheDir, retryTestAssetName), err
}

// useShortRetryDelay shortens the retry wait and replaces the GitHub
// attestation check, which needs a release published by GitHub Actions. The
// download and the checksum verification run for real.
func useShortRetryDelay(t *testing.T) {
	t.Helper()
	originalDelay := downloadRetryDelay
	originalAttestations := updateVerifyGitHubAttestations
	t.Cleanup(func() {
		downloadRetryDelay = originalDelay
		updateVerifyGitHubAttestations = originalAttestations
	})
	downloadRetryDelay = time.Millisecond
	updateVerifyGitHubAttestations = func(_ context.Context, _ Options, _ release, _ releaseAsset, _ string) error {
		return nil
	}
}

func TestDownloadRetriesServerErrorThenVerifiesChecksum(t *testing.T) {
	useShortRetryDelay(t)
	archive := []byte("release archive")
	flaky := newFlakyAssetServer(t, archive, archive, []int{http.StatusInternalServerError, http.StatusBadGateway})

	archivePath, err := flaky.verify(t)
	if err != nil {
		t.Fatalf("VerifyReleaseAssets() error: %v", err)
	}
	if got := flaky.downloadRequests(); got != 3 {
		t.Fatalf("download requests = %d, want 3", got)
	}
	assertFileBytes(t, archivePath, archive)
}

func TestDownloadStopsAfterBoundedServerErrors(t *testing.T) {
	useShortRetryDelay(t)
	archive := []byte("release archive")
	flaky := newFlakyAssetServer(t, archive, archive, []int{
		http.StatusInternalServerError, http.StatusInternalServerError,
		http.StatusInternalServerError, http.StatusInternalServerError,
	})

	_, err := flaky.verify(t)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("VerifyReleaseAssets() error = %v, want HTTP 500", err)
	}
	if got := flaky.downloadRequests(); got != downloadAttempts {
		t.Fatalf("download requests = %d, want %d", got, downloadAttempts)
	}
}

func TestDownloadRetriesTruncatedResponseBody(t *testing.T) {
	useShortRetryDelay(t)
	archive := []byte("release archive")
	flaky := newFlakyAssetServer(t, archive, archive, []int{truncatedBody})

	archivePath, err := flaky.verify(t)
	if err != nil {
		t.Fatalf("VerifyReleaseAssets() error: %v", err)
	}
	if got := flaky.downloadRequests(); got != 2 {
		t.Fatalf("download requests = %d, want 2", got)
	}
	assertFileBytes(t, archivePath, archive)
}

func TestDownloadDoesNotRetryClientError(t *testing.T) {
	useShortRetryDelay(t)
	archive := []byte("release archive")
	flaky := newFlakyAssetServer(t, archive, archive, []int{http.StatusNotFound})

	_, err := flaky.verify(t)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("VerifyReleaseAssets() error = %v, want HTTP 404", err)
	}
	if got := flaky.downloadRequests(); got != 1 {
		t.Fatalf("download requests = %d, want 1", got)
	}
}

func TestDownloadAfterRetryStillVerifiesChecksum(t *testing.T) {
	useShortRetryDelay(t)
	archive := []byte("release archive")
	flaky := newFlakyAssetServer(t, archive, []byte("tampered archive"), []int{http.StatusServiceUnavailable})

	_, err := flaky.verify(t)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("VerifyReleaseAssets() error = %v, want checksum mismatch", err)
	}
	if got := flaky.downloadRequests(); got != 2 {
		t.Fatalf("download requests = %d, want 2", got)
	}
}
