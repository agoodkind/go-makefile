package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand"
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
	server := newFetchServer(t, map[string]string{"go.mk": "__GO_MK_FILE := 1\n"})

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

	server.SetFiles(map[string]string{"go.mk": "__GO_MK_FILE := 2\n"})
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
	server := newFetchServer(t, map[string]string{"go.mk": "__GO_MK_FILE := 1\n"})

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
	if got, want := string(extractedContent), "__GO_MK_FILE := 1\n"; got != want {
		t.Fatalf("extracted go.mk = %q, want %q", got, want)
	}
}

// fetchRequest records one served request so a test can assert how many times
// the helper hit the network, which HTTP method it used, and what each call
// returned.
type fetchRequest struct {
	Method string
	// Path is the request URI the helper actually asked for. Without it no
	// test can tell a correct fetch from one aimed at the wrong repository or
	// ref, because this server answers every path with the same fixture, so a
	// regression in either would still return a normal 200.
	Path        string
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

	mutex                 sync.Mutex
	tarball               []byte
	etag                  string
	etagDisabled          bool
	stall                 time.Duration
	trickleBytesPerSecond int
	requests              []fetchRequest
	server                *httptest.Server
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

// Trickle makes a 200 response body write in chunks of bytesPerSecond,
// pausing one second between chunks, rather than in a single Write call.
// This is distinct from Stall: a stalled connection produces no bytes at
// all, while a trickled one keeps making genuine progress just slowly, so
// a test can tell "curl aborts a truly stuck transfer" apart from "curl
// does not punish a merely slow one" against the same speed-limit flags.
func (s *fetchServer) Trickle(bytesPerSecond int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.trickleBytesPerSecond = bytesPerSecond
}

// SetETagEnabled controls whether handle sets the ETag response header and
// ever answers 304, so a test can simulate an upstream 200 response that
// carries no ETag at all.
func (s *fetchServer) SetETagEnabled(enabled bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.etagDisabled = !enabled
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
	etagDisabled := s.etagDisabled
	stall := s.stall
	trickleBytesPerSecond := s.trickleBytesPerSecond
	s.mutex.Unlock()

	if stall > 0 {
		time.Sleep(stall)
	}

	record := fetchRequest{
		Method:      request.Method,
		Path:        request.URL.Path,
		IfNoneMatch: request.Header.Get("If-None-Match"),
	}
	if !etagDisabled {
		writer.Header().Set("ETag", etag)
	}
	if !etagDisabled && record.IfNoneMatch == etag {
		record.Status = http.StatusNotModified
		writer.WriteHeader(http.StatusNotModified)
	} else {
		record.Status = http.StatusOK
		writer.Header().Set("Content-Type", "application/x-gzip")
		writer.WriteHeader(http.StatusOK)
		// Record what was actually written, not len(tarball). net/http
		// discards the body of a HEAD response, so an intended count would
		// report a full transfer for a request that carried no bytes at all,
		// and an assertion that a real download happened would pass against a
		// HEAD probe.
		if trickleBytesPerSecond > 0 {
			record.Bytes = writeTrickled(writer, tarball, trickleBytesPerSecond)
		} else {
			written, _ := writer.Write(tarball)
			record.Bytes = written
		}
		if request.Method == http.MethodHead {
			record.Bytes = 0
		}
	}

	s.mutex.Lock()
	s.requests = append(s.requests, record)
	s.mutex.Unlock()
}

// writeTrickled writes body in bytesPerSecond-sized chunks, flushing each
// one and sleeping a second before the next, so a client sees genuine
// incremental progress spread over multiple seconds rather than the whole
// body arriving at once.
// writeTrickled returns the number of bytes it actually wrote, so a caller can
// record real transfer rather than intent.
func writeTrickled(writer io.Writer, body []byte, bytesPerSecond int) int {
	written := 0
	flusher, canFlush := writer.(http.Flusher)
	for offset := 0; offset < len(body); offset += bytesPerSecond {
		end := offset + bytesPerSecond
		if end > len(body) {
			end = len(body)
		}
		count, err := writer.Write(body[offset:end])
		written += count
		if err != nil {
			return written
		}
		if canFlush {
			flusher.Flush()
		}
		if end < len(body) {
			time.Sleep(time.Second)
		}
	}
	return written
}

// randomAssetBody returns byteCount pseudo-random bytes from a fixed seed, so
// a test's tarball is reproducible while staying close to incompressible:
// gzip cannot shrink it the way it would a repeated-character fixture, so a
// trickled transfer of it takes close to byteCount/rate seconds rather than
// finishing as soon as gzip collapses padding down to nothing.
func randomAssetBody(byteCount int) string {
	source := rand.New(rand.NewSource(1))
	body := make([]byte, byteCount)
	_, _ = source.Read(body)
	return string(body)
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
