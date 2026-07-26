package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
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

func TestFetchServerServesTarballAndHonorsIfNoneMatch(t *testing.T) {
	server := newFetchServer(t, map[string]string{"go.mk": "GO_MK := 1\n"})

	url := server.CodeloadBase() + "/agoodkind/go-makefile/tar.gz/main"
	first, err := http.Get(url)
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	defer func() { _ = first.Body.Close() }()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode)
	}
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("first response carried no ETag")
	}
	if got := tarEntryNames(t, first.Body); !strings.Contains(got, "go.mk") {
		t.Fatalf("tarball entries = %q, want one containing go.mk", got)
	}

	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build conditional request: %v", err)
	}
	request.Header.Set("If-None-Match", etag)
	second, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", second.StatusCode)
	}

	server.SetFiles(map[string]string{"go.mk": "GO_MK := 2\n"})
	third, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post-advance GET: %v", err)
	}
	defer func() { _ = third.Body.Close() }()
	if third.StatusCode != http.StatusOK {
		t.Fatalf("post-advance status = %d, want 200 after content changed", third.StatusCode)
	}

	if len(server.Requests()) != 3 {
		t.Fatalf("recorded %d requests, want 3", len(server.Requests()))
	}
}

// tarEntryNames reads a gzipped tarball and returns its entry names joined by
// newlines, so a test can assert the archive layout the helper will extract.
func tarEntryNames(t *testing.T, body io.Reader) string {
	t.Helper()
	gzipReader, err := gzip.NewReader(body)
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	defer func() { _ = gzipReader.Close() }()
	var names []string
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		names = append(names, header.Name)
	}
	return strings.Join(names, "\n")
}

// TestFetchServerTarballExtractsWithSystemTar exercises the archive through
// the real system tar binary rather than only the Go tar reader. The
// bootstrap helper shells out to tar --strip-components 1, so a test that
// only inspects entry names in Go would not catch an archive that Go's
// writer produced but the system tar rejects or extracts incorrectly.
func TestFetchServerTarballExtractsWithSystemTar(t *testing.T) {
	server := newFetchServer(t, map[string]string{"go.mk": "GO_MK := 1\n"})

	url := server.CodeloadBase() + "/agoodkind/go-makefile/tar.gz/main"
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	archiveBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read archive body: %v", err)
	}

	archiveDir := t.TempDir()
	archivePath := filepath.Join(archiveDir, "archive.tar.gz")
	if err := os.WriteFile(archivePath, archiveBytes, 0o644); err != nil {
		t.Fatalf("write archive to temp file: %v", err)
	}

	extractDir := t.TempDir()
	cmd := exec.Command("tar", "-xzf", archivePath, "-C", extractDir, "--strip-components", "1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar extract: %v\n%s", err, output)
	}

	extractedPath := filepath.Join(extractDir, "go.mk")
	extractedContent, err := os.ReadFile(extractedPath)
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if got, want := string(extractedContent), "GO_MK := 1\n"; got != want {
		t.Fatalf("extracted go.mk = %q, want %q", got, want)
	}
}

// fetchRequest records one served request so a test can assert how many times
// the helper hit the network and what each call returned.
type fetchRequest struct {
	IfNoneMatch string
	Status      int
	Bytes       int
}

// fetchServer stands in for codeload.github.com. It builds a real gzipped
// tarball whose single top-level directory matches the archive layout the
// helper extracts with --strip-components 1, and answers conditional requests
// the way codeload does: an ETag on 200, and 304 with an empty body when the
// caller's If-None-Match still matches the current content.
type fetchServer struct {
	URL string

	mutex    sync.Mutex
	tarball  []byte
	etag     string
	stall    time.Duration
	requests []fetchRequest
	server   *httptest.Server
}

func newFetchServer(t *testing.T, files map[string]string) *fetchServer {
	t.Helper()
	server := &fetchServer{}
	server.SetFiles(files)
	server.server = httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(server.server.Close)
	server.URL = server.server.URL
	return server
}

func (s *fetchServer) CodeloadBase() string {
	return s.URL
}

// SetFiles replaces the served tree and recomputes the ETag, so a test can
// simulate upstream moving.
func (s *fetchServer) SetFiles(files map[string]string) {
	tarball := buildTarball(files)
	digest := sha256.Sum256(tarball)
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.tarball = tarball
	s.etag = `"` + hex.EncodeToString(digest[:]) + `"`
}

func (s *fetchServer) Stall(d time.Duration) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.stall = d
}

func (s *fetchServer) Requests() []fetchRequest {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]fetchRequest(nil), s.requests...)
}

func (s *fetchServer) handle(writer http.ResponseWriter, request *http.Request) {
	s.mutex.Lock()
	tarball := s.tarball
	etag := s.etag
	stall := s.stall
	s.mutex.Unlock()

	if stall > 0 {
		time.Sleep(stall)
	}

	record := fetchRequest{IfNoneMatch: request.Header.Get("If-None-Match")}
	writer.Header().Set("ETag", etag)
	if record.IfNoneMatch == etag {
		record.Status = http.StatusNotModified
		writer.WriteHeader(http.StatusNotModified)
	} else {
		record.Status = http.StatusOK
		record.Bytes = len(tarball)
		writer.Header().Set("Content-Type", "application/x-gzip")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(tarball)
	}

	s.mutex.Lock()
	s.requests = append(s.requests, record)
	s.mutex.Unlock()
}

// buildTarball produces a gzipped tar whose entries all sit under one top-level
// directory, matching a GitHub source archive. Entries are sorted so the same
// file set always produces the same bytes and therefore the same ETag.
func buildTarball(files map[string]string) []byte {
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		body := files[name]
		header := &tar.Header{
			Name:    "go-makefile-test/" + name,
			Mode:    0o644,
			Size:    int64(len(body)),
			ModTime: time.Unix(0, 0),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			panic(err)
		}
		if _, err := tarWriter.Write([]byte(body)); err != nil {
			panic(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		panic(err)
	}
	if err := gzipWriter.Close(); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}
