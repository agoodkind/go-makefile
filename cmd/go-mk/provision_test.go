package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type releaseServer struct {
	URL string

	mutex                 sync.Mutex
	archives              map[string][]byte
	etags                 map[string]string
	etagDisabled          bool
	stall                 time.Duration
	trickleBytesPerSecond int
	requests              []fetchRequest
	server                *httptest.Server
}

func newReleaseServer(t *testing.T, sourceFiles map[string]string) *releaseServer {
	t.Helper()
	server := &releaseServer{
		archives: map[string][]byte{},
		etags:    map[string]string{},
	}
	server.SetSourceFiles(sourceFiles)
	server.server = httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(server.server.Close)
	server.URL = server.server.URL
	return server
}

func (server *releaseServer) SetSourceFiles(sourceFiles map[string]string) {
	sourceArchive, sourceETag := buildProvisionSourceArchive(sourceFiles)
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.archives["/agoodkind/go-makefile/releases/download/rolling/go-makefile-src.tar.gz"] = sourceArchive
	server.etags["/agoodkind/go-makefile/releases/download/rolling/go-makefile-src.tar.gz"] = sourceETag
}

func (server *releaseServer) SetFiles(sourceFiles map[string]string) {
	server.SetSourceFiles(sourceFiles)
}

func (server *releaseServer) SetCodeloadRef(ref string, sourceFiles map[string]string) {
	tarball := buildTarball(sourceFiles)
	digest := sha256.Sum256(tarball)
	path := fmt.Sprintf("/agoodkind/go-makefile/tar.gz/%s", ref)
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.archives[path] = tarball
	server.etags[path] = `"` + hex.EncodeToString(digest[:]) + `"`
}

func (server *releaseServer) ReleaseBase() string {
	return server.URL
}

func (server *releaseServer) Stall(duration time.Duration) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.stall = duration
}

func (server *releaseServer) Trickle(bytesPerSecond int) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.trickleBytesPerSecond = bytesPerSecond
}

func (server *releaseServer) SetETagEnabled(enabled bool) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.etagDisabled = !enabled
}

func (server *releaseServer) Requests() []fetchRequest {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return append([]fetchRequest(nil), server.requests...)
}

func (server *releaseServer) handle(writer http.ResponseWriter, request *http.Request) {
	server.mutex.Lock()
	body := server.archives[request.URL.Path]
	etag := server.etags[request.URL.Path]
	etagDisabled := server.etagDisabled
	stall := server.stall
	trickleBytesPerSecond := server.trickleBytesPerSecond
	server.mutex.Unlock()

	if stall > 0 {
		time.Sleep(stall)
	}

	record := fetchRequest{
		Method:      request.Method,
		Path:        request.URL.Path,
		IfNoneMatch: request.Header.Get("If-None-Match"),
	}
	if !etagDisabled && etag != "" {
		writer.Header().Set("ETag", etag)
	}
	if !etagDisabled && etag != "" && record.IfNoneMatch == etag {
		record.Status = http.StatusNotModified
		writer.WriteHeader(http.StatusNotModified)
	} else if body == nil {
		record.Status = http.StatusNotFound
		writer.WriteHeader(http.StatusNotFound)
	} else {
		record.Status = http.StatusOK
		writer.Header().Set("Content-Type", "application/gzip")
		writer.WriteHeader(http.StatusOK)
		if trickleBytesPerSecond > 0 {
			record.Bytes = writeTrickled(writer, body, trickleBytesPerSecond)
		} else {
			written, _ := writer.Write(body)
			record.Bytes = written
		}
		if request.Method == http.MethodHead {
			record.Bytes = 0
		}
	}
	server.mutex.Lock()
	server.requests = append(server.requests, record)
	server.mutex.Unlock()
}

func TestProvisionUsesRollingReleaseURL(t *testing.T) {
	server := newReleaseServer(t, helperFiles())
	dir := t.TempDir()
	if err := seedProvisionBinary(t, dir); err != nil {
		t.Fatalf("seedProvisionBinary: %v", err)
	}

	code := runProvisionInDir(t, dir, map[string]string{
		"GO_MK_RELEASE_BASE": server.ReleaseBase(),
		"GO_MK_API_REF":      "main",
	})
	if code != 0 {
		t.Fatalf("provision exit = %d, want 0", code)
	}
	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(requests))
	}
	wantPath := "/agoodkind/go-makefile/releases/download/rolling/go-makefile-src.tar.gz"
	if requests[0].Path != wantPath {
		t.Fatalf("request path = %q, want %q", requests[0].Path, wantPath)
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want served body", got)
	}
}

func TestProvisionPinRefFallsBackToCodeload(t *testing.T) {
	releaseServer := newReleaseServer(t, helperFiles())
	codeloadServer := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	if err := seedProvisionBinary(t, dir); err != nil {
		t.Fatalf("seedProvisionBinary: %v", err)
	}

	code := runProvisionInDir(t, dir, map[string]string{
		"GO_MK_RELEASE_BASE":  releaseServer.ReleaseBase(),
		"GO_MK_CODELOAD_BASE": codeloadServer.CodeloadBase(),
		"GO_MK_API_REF":       "deadbeef",
	})
	if code != 0 {
		t.Fatalf("provision exit = %d, want 0", code)
	}
	releaseRequests := releaseServer.Requests()
	if len(releaseRequests) != 1 || releaseRequests[0].Status != http.StatusNotFound {
		t.Fatalf("release requests = %#v, want one 404", releaseRequests)
	}
	codeloadRequests := codeloadServer.Requests()
	if len(codeloadRequests) != 1 {
		t.Fatalf("codeload requests = %d, want 1", len(codeloadRequests))
	}
}

func runProvisionInDir(t *testing.T, dir string, env map[string]string) int {
	t.Helper()
	binaryPath := buildGoMkForTest(t)
	command := exec.Command(binaryPath, "provision")
	command.Dir = dir
	defaults := map[string]string{
		"GO_MK_API_REPO": "agoodkind/go-makefile",
		"GO_MK_API_REF":  "main",
	}
	for key, value := range env {
		defaults[key] = value
	}
	command.Env = testProcessEnvironment(defaults)
	output, err := command.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Logf("provision stderr/stdout:\n%s", output)
			return exitErr.ExitCode()
		}
		t.Fatalf("run provision: %v\n%s", err, output)
	}
	return 0
}

func seedProvisionBinary(t *testing.T, dir string) error {
	t.Helper()
	existing := filepath.Join(dir, ".make", "go-mk")
	if info, err := os.Stat(existing); err == nil && info.Size() > 0 && goMkHasCommand(t, existing, "resolve-bin") {
		return nil
	}
	binaryPath := buildGoMkForTest(t)
	if err := os.MkdirAll(filepath.Join(dir, ".make"), 0o755); err != nil {
		return err
	}
	input, err := os.ReadFile(binaryPath)
	if err != nil {
		return err
	}
	return os.WriteFile(existing, input, 0o755)
}

func goMkHasCommand(t *testing.T, binaryPath string, command string) bool {
	t.Helper()
	output, err := exec.Command(binaryPath, "-flags").CombinedOutput()
	if err != nil {
		return false
	}
	want := "Name: " + command
	for _, line := range strings.Split(string(output), "\n") {
		if line == want {
			return true
		}
	}
	return false
}

func buildGoMkForTest(t *testing.T) string {
	t.Helper()
	outputPath := filepath.Join(t.TempDir(), "go-mk")
	command := exec.Command("go", "build", "-o", outputPath, "./cmd/go-mk")
	command.Dir = repoRootForTest(t)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build go-mk for test: %v\n%s", err, output)
	}
	return outputPath
}

func checksumsForBinaryArchive(t *testing.T, archiveBytes []byte, archiveName string) string {
	t.Helper()
	digest := sha256.Sum256(archiveBytes)
	return hex.EncodeToString(digest[:]) + "  " + archiveName + "\n"
}

func buildProvisionSourceArchive(files map[string]string) ([]byte, string) {
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := files[name]
		header := &tar.Header{
			Name: filepath.ToSlash(filepath.Join("go-makefile-src", name)),
			Mode: int64(0o644),
			Size: int64(len(content)),
		}
		if strings.HasSuffix(name, ".sh") {
			header.Mode = 0o755
		}
		_ = tarWriter.WriteHeader(header)
		_, _ = tarWriter.Write([]byte(content))
	}
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	archive := buffer.Bytes()
	digest := sha256.Sum256(archive)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	return archive, etag
}

