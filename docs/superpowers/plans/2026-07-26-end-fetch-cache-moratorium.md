# End the Fetch Cache Moratorium Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all go-makefile fetch policy out of the consumer-committed `bootstrap.mk` into a fetched helper that validates the engine tarball with a conditional request, reuses `.make/` when upstream has not moved, and never deletes an asset it cannot replace.

**Architecture:** A new shell helper, `scripts/go-mk-bootstrap.sh`, owns provisioning. It sends one conditional `GET` to codeload carrying `If-None-Match`; a `304` means the extracted tree is byte-identical and nothing is transferred, a `200` is staged in a temp directory and swapped in only after every asset verifies. `bootstrap.mk` shrinks to variables, one non-destructive helper acquisition, and the include, so future policy changes need no consumer PR.

**Tech Stack:** GNU Make, Bash, curl, Go 1.25 tests using `net/http/httptest`, `archive/tar`, and `os/exec`.

## Global Constraints

- Validation request bound: `--connect-timeout 2 --max-time 3`. Measured `304` is 0.88s median on a 201ms RTT link.
- Reuse window after a failed validation: 3600 seconds (1 hour), a hardcoded constant, not a variable. The clock runs from the last completed download and a successful `304` never resets it, so the window is fixed rather than sliding. Offline reuse is therefore available only within an hour of a real download, which is the intended strictness.
- A `304` writes nothing: no asset changes, and the state file is not rewritten.
- CI test: `GITHUB_ACTIONS` equals `true` AND `GITHUB_RUN_ID` is non-empty. `GITHUB_ACTIONS` alone is not CI.
- In CI: never read state, never write state, never send a conditional request, never serve disk on failure.
- No new user-facing variable. `GO_MK_SKIP_FETCH` stays the only knob. `GO_MK_CODELOAD_BASE` is internal and test-only, in the same category as the existing `GO_MK_API_REPO` and `GO_MK_API_REF` overrides.
- Never delete an asset before its verified replacement exists.
- State file path: `.make/.go-mk-fetch-state`, with the exact keys `ref=`, `etag=`, `timestamp=`.
- Shell rules: `#!/usr/bin/env bash`, `set -euo pipefail`, `[[ ]]` tests, 4-space indent, `local` inside functions, snake_case functions and locals, UPPER_CASE constants, full `if / then / fi` blocks.
- Spec: `docs/superpowers/specs/2026-07-26-end-fetch-cache-moratorium-design.md`.

---

## File Structure

**Created:**

- `scripts/go-mk-bootstrap.sh` is the helper. It owns the decision table, staged provisioning, state read and write, and the CI rule. Its single responsibility is to put a correct set of assets under `.make/` or fail loudly.
- `cmd/go-mk/fetchserver_test.go` is the shared test harness. It serves a real gzipped tarball over `httptest`, computes an `ETag`, honors `If-None-Match`, counts requests, and can be told to stall or to advance its content.
- `cmd/go-mk/bootstrapfetch_test.go` holds the tests that invoke the helper directly.
- `cmd/go-mk/bootstrapparse_test.go` holds the end-to-end tests that run `make` in a temp consumer.
- `docs/fetch.md` states the fetch contract as current-state behavior.

**Modified:**

- `bootstrap.mk` is reduced to variables, helper acquisition, and the include.
- `cmd/go-mk/bootstrap_assets/bootstrap.mk` is the embedded copy `reconcileBootstrapMk` writes, and must stay byte-identical to `bootstrap.mk`.
- `go.mk:10,11,15` loses `GO_MK_URL`, `GO_MK_CACHE`, and `GO_MK_CACHE_DIR`, and skips its own provisioning when the helper already ran.
- `scripts/go-mk-fetch-one.sh:57` drops the moratorium wording from its failure message.

---

### Task 1: Tarball test harness

The harness every later task tests against. It is worth its own task because a wrong harness silently invalidates every test built on it, so it gets its own gate.

**Files:**
- Create: `cmd/go-mk/fetchserver_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type fetchServer struct { URL string; ... }`
  - `func newFetchServer(t *testing.T, files map[string]string) *fetchServer`
  - `func (s *fetchServer) SetFiles(files map[string]string)` advances the served content and the `ETag`.
  - `func (s *fetchServer) Stall(d time.Duration)` makes every later request sleep before responding.
  - `func (s *fetchServer) Requests() []fetchRequest` where `type fetchRequest struct { IfNoneMatch string; Status int; Bytes int }`
  - `func (s *fetchServer) CodeloadBase() string` returns the value to pass as `GO_MK_CODELOAD_BASE`.

- [ ] **Step 1: Write the failing test**

Create `cmd/go-mk/fetchserver_test.go` with the harness test first. The harness itself follows in Step 3.

```go
package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/agoodkind/.worktrees/-Users-agoodkind-Sites-go-makefile/end-fetch-cache-moratorium && go test ./cmd/go-mk/ -run TestFetchServer -v`

Expected: FAIL to build with `undefined: newFetchServer`.

- [ ] **Step 3: Write minimal implementation**

Append the harness to the same file, adding these imports to the existing block: `bytes`, `crypto/sha256`, `encoding/hex`, `net/http/httptest`, `sort`, `sync`, `time`.

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/go-mk/ -run TestFetchServer -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/go-mk/fetchserver_test.go
git commit -S -m "Add tarball test server harness for fetch tests

Co-authored-by: Claude <noreply@anthropic.com>"
```

---

### Task 2: Helper cold provision, staged and non-destructive

**Files:**
- Create: `scripts/go-mk-bootstrap.sh`
- Create: `cmd/go-mk/bootstrapfetch_test.go`

**Interfaces:**
- Consumes: `newFetchServer`, `fetchServer.CodeloadBase`, `fetchServer.Requests` from Task 1.
- Produces:
  - The helper's contract. Run `bash scripts/go-mk-bootstrap.sh` with the working directory at the consumer root. It reads `GO_MK_API_REPO`, `GO_MK_API_REF`, `GO_MK_CODELOAD_BASE`, `GO_MK_DEV_DIR`, `GO_MK_MODULES`, and `GO_MK_SKIP_FETCH`. It exits 0 with every asset under `.make/`, or non-zero with a message on stderr.
  - `func runHelper(t *testing.T, dir string, env map[string]string) (stdout string, stderr string, exitCode int)` in `bootstrapfetch_test.go`.
  - `func writeAsset(t *testing.T, dir string, relative string, body string)` in `bootstrapfetch_test.go`.
  - `func readAsset(t *testing.T, dir string, relative string) string` in `bootstrapfetch_test.go`.
  - `func helperFiles() map[string]string` in `bootstrapfetch_test.go`.
  - `func repoRootForTest(t *testing.T) string` in `bootstrapfetch_test.go`.
  - `func asExitError(err error, target **exec.ExitError) bool` in `bootstrapfetch_test.go`.

- [ ] **Step 1: Write the failing test**

Create `cmd/go-mk/bootstrapfetch_test.go`.

```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// helperFiles is the engine tree the test server serves. It carries every asset
// the helper must provision, so a cold run has a complete source.
func helperFiles() map[string]string {
	return map[string]string{
		"go.mk":                      "# go.mk v1\n",
		"golangci.yml":               "version: \"2\"\n",
		"notices.txt":                "notice v1\n",
		"scripts/go-mk-fetch-one.sh": "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-bin.sh":       "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-sync.sh":      "#!/usr/bin/env bash\nexit 0\n",
		"scripts/go-mk-bootstrap.sh": "#!/usr/bin/env bash\nexit 0\n",
	}
}

func TestHelperColdProvisionWritesEveryAsset(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	stdout, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	for _, asset := range []string{"go.mk", "golangci.yml", "notices.txt", "scripts/go-mk-fetch-one.sh"} {
		if body := readAsset(t, dir, asset); body == "" {
			t.Fatalf("asset %s missing or empty after cold provision", asset)
		}
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the served body", got)
	}
	if requests := server.Requests(); len(requests) != 1 {
		t.Fatalf("server saw %d requests, want exactly 1", len(requests))
	}
}

func TestHelperLeavesAssetsIntactWhenUpstreamIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	writeAsset(t, dir, "go.mk", "# warm go.mk\n")
	writeAsset(t, dir, "golangci.yml", "version: \"2\"\n")
	writeAsset(t, dir, "notices.txt", "warm notice\n")
	writeAsset(t, dir, "scripts/go-mk-fetch-one.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-bin.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeAsset(t, dir, "scripts/go-mk-sync.sh", "#!/usr/bin/env bash\nexit 0\n")

	_, _, code := runHelper(t, dir, map[string]string{
		// A port nothing listens on, so every request fails at connect.
		"GO_MK_CODELOAD_BASE": "http://127.0.0.1:9",
	})
	if code == 0 {
		t.Fatal("helper exit = 0, want non-zero with no state and an unreachable upstream")
	}

	// The point of the task: a failed fetch must not remove what was there.
	if got := readAsset(t, dir, "go.mk"); got != "# warm go.mk\n" {
		t.Fatalf("go.mk = %q after a failed fetch, want the original body preserved", got)
	}
	if got := readAsset(t, dir, "golangci.yml"); got == "" {
		t.Fatal("golangci.yml was destroyed by a failed fetch")
	}
}

// runHelper executes the helper with the working directory at dir and a
// minimal environment, and returns its output and exit code.
func runHelper(t *testing.T, dir string, env map[string]string) (string, string, int) {
	t.Helper()
	helper := filepath.Join(repoRootForTest(t), "scripts", "go-mk-bootstrap.sh")
	command := exec.Command("bash", helper)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"GO_MK_API_REPO=agoodkind/go-makefile",
		"GO_MK_API_REF=main",
		"GO_MK_DEV_DIR=",
		"GO_MK_MODULES=",
		// Never let the test inherit a real CI environment.
		"GITHUB_ACTIONS=",
		"GITHUB_RUN_ID=",
	)
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			t.Fatalf("run helper: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func asExitError(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
}

func writeAsset(t *testing.T, dir string, relative string, body string) {
	t.Helper()
	path := filepath.Join(dir, ".make", relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func readAsset(t *testing.T, dir string, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, ".make", relative))
	if err != nil {
		return ""
	}
	return string(body)
}

// repoRootForTest resolves the engine checkout so a test can invoke a script by
// its committed path regardless of the temp working directory.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return strings.TrimSpace(string(output))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/go-mk/ -run TestHelper -v`
Expected: FAIL, both tests, because `scripts/go-mk-bootstrap.sh` does not exist so `bash` exits 127.

- [ ] **Step 3: Write minimal implementation**

Create `scripts/go-mk-bootstrap.sh`.

```bash
#!/usr/bin/env bash
# go-mk-bootstrap.sh: provision every go-makefile asset into .make.
#
# bootstrap.mk delegates here so fetch policy lives in a fetched file rather
# than in the copy each consumer commits. A policy change therefore ships to
# every consumer on its next run, with no consumer pull request.
#
# Provisioning is staged: one tarball extracts into a temp directory, every
# required asset is verified there, and only then are the files under .make
# replaced. Nothing is deleted before its replacement exists, so a failed or
# partial download leaves the previous assets exactly as they were.

set -euo pipefail

GO_MK_API_REPO="${GO_MK_API_REPO:-agoodkind/go-makefile}"
GO_MK_API_REF="${GO_MK_API_REF:-main}"
# Internal override, in the same category as GO_MK_API_REPO and GO_MK_API_REF.
# Tests point it at a local server; consumers never set it.
GO_MK_CODELOAD_BASE="${GO_MK_CODELOAD_BASE:-https://codeload.github.com}"
GO_MK_DEV_DIR="${GO_MK_DEV_DIR:-}"
GO_MK_MODULES="${GO_MK_MODULES:-}"

MAKE_DIR=".make"
FETCH_MAX_TIME=30

required_assets() {
    printf '%s\n' "go.mk"
    printf '%s\n' "golangci.yml"
    printf '%s\n' "notices.txt"
    printf '%s\n' "scripts/go-mk-fetch-one.sh"
    printf '%s\n' "scripts/go-mk-bin.sh"
    printf '%s\n' "scripts/go-mk-sync.sh"
    local module_name
    for module_name in ${GO_MK_MODULES}; do
        printf '%s\n' "${module_name}"
    done
}

assets_complete() {
    local base_dir="$1"
    local asset_name
    while IFS= read -r asset_name; do
        if [[ ! -s "${base_dir}/${asset_name}" ]]; then
            return 1
        fi
    done < <(required_assets)
    return 0
}

# install_from_stage copies each verified asset out of the staging tree into
# .make. It runs only after assets_complete succeeded against the stage, so a
# copy here always overwrites a file with known-good content.
install_from_stage() {
    local stage_dir="$1"
    local asset_name
    local target_path
    while IFS= read -r asset_name; do
        target_path="${MAKE_DIR}/${asset_name}"
        mkdir -p "$(dirname "${target_path}")"
        cp "${stage_dir}/${asset_name}" "${target_path}"
        case "${target_path}" in
            *.sh) chmod +x "${target_path}" ;;
        esac
    done < <(required_assets)
}

install_from_dev_dir() {
    local asset_name
    local target_path
    while IFS= read -r asset_name; do
        if [[ ! -f "${GO_MK_DEV_DIR}/${asset_name}" ]]; then
            continue
        fi
        target_path="${MAKE_DIR}/${asset_name}"
        mkdir -p "$(dirname "${target_path}")"
        cp "${GO_MK_DEV_DIR}/${asset_name}" "${target_path}"
        case "${target_path}" in
            *.sh) chmod +x "${target_path}" ;;
        esac
    done < <(required_assets)
}

# provision downloads, extracts into a stage, verifies, then installs.
provision() {
    local stage_root
    local stage_dir
    local status_code

    stage_root=$(mktemp -d "${TMPDIR:-/tmp}/go-mk-stage.XXXXXXXX") || return 1
    trap 'rm -rf "${stage_root}"' RETURN

    if ! status_code=$(curl -sS --connect-timeout 5 --max-time "${FETCH_MAX_TIME}" \
        --retry 3 --retry-delay 2 \
        -o "${stage_root}/snapshot.tar.gz" -w '%{http_code}' \
        "${GO_MK_CODELOAD_BASE}/${GO_MK_API_REPO}/tar.gz/${GO_MK_API_REF}" 2>/dev/null); then
        return 1
    fi
    if [[ "${status_code}" != "200" ]]; then
        return 1
    fi

    stage_dir="${stage_root}/tree"
    mkdir -p "${stage_dir}"
    if ! tar -xzf "${stage_root}/snapshot.tar.gz" -C "${stage_dir}" --strip-components 1 2>/dev/null; then
        return 1
    fi
    if ! assets_complete "${stage_dir}"; then
        return 1
    fi
    install_from_stage "${stage_dir}"
    return 0
}

main() {
    mkdir -p "${MAKE_DIR}"

    if [[ -n "${GO_MK_DEV_DIR}" ]]; then
        install_from_dev_dir
        return 0
    fi

    if [[ "${GO_MK_SKIP_FETCH:-}" == "1" ]]; then
        if assets_complete "${MAKE_DIR}"; then
            return 0
        fi
        printf '%s\n' "error: GO_MK_SKIP_FETCH=1 but .make is missing a required asset" >&2
        return 1
    fi

    if provision; then
        return 0
    fi

    printf '%s\n' "error: could not provision go-makefile assets. Set GO_MK_DEV_DIR, or check network access to ${GO_MK_CODELOAD_BASE}" >&2
    return 1
}

main "$@"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/go-mk/ -run TestHelper -v`
Expected: PASS, both tests.

- [ ] **Step 5: Commit**

```bash
chmod +x scripts/go-mk-bootstrap.sh
git add scripts/go-mk-bootstrap.sh cmd/go-mk/bootstrapfetch_test.go
git commit -S -m "Add go-mk-bootstrap.sh with staged non-destructive provisioning

Co-authored-by: Claude <noreply@anthropic.com>"
```

---

### Task 3: Conditional validation and fetch state

**Files:**
- Modify: `scripts/go-mk-bootstrap.sh`
- Modify: `cmd/go-mk/bootstrapfetch_test.go`

**Interfaces:**
- Consumes: the helper contract and test helpers from Task 2.
- Produces:
  - `.make/.go-mk-fetch-state` with exactly three lines: `ref=<value>`, `etag=<value>`, `timestamp=<unix seconds>`.
  - Shell functions `read_state_field`, `write_state`, `current_epoch_seconds`, and `validate_upstream` inside the helper.
  - `func readState(t *testing.T, dir string) map[string]string` in `bootstrapfetch_test.go`.
  - `func writeState(t *testing.T, dir string, ref string, etag string, timestamp int64)` in `bootstrapfetch_test.go`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/go-mk/bootstrapfetch_test.go`, adding these imports: `bufio`, `fmt`, `strconv`, `time`.

```go
func TestHelperColdProvisionRecordsState(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0: %s", code, stderr)
	}

	state := readState(t, dir)
	if state["ref"] != "main" {
		t.Fatalf("state ref = %q, want main", state["ref"])
	}
	if state["etag"] == "" {
		t.Fatal("state carried no etag after a cold provision")
	}
	stamp, err := strconv.ParseInt(state["timestamp"], 10, 64)
	if err != nil {
		t.Fatalf("state timestamp %q is not an integer: %v", state["timestamp"], err)
	}
	if time.Since(time.Unix(stamp, 0)) > time.Minute {
		t.Fatalf("state timestamp %d is not recent", stamp)
	}
}

func TestHelperServesDiskWhenUpstreamReturnsNotModified(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	// Cold run populates .make and records the ETag.
	_, _, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("cold run exit = %d, want 0", code)
	}
	// Mark the on-disk copy so a re-extract would be visible.
	writeAsset(t, dir, "go.mk", "# locally marked\n")

	_, stderr, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("warm run exit = %d, want 0: %s", code, stderr)
	}

	if got := readAsset(t, dir, "go.mk"); got != "# locally marked\n" {
		t.Fatalf("go.mk = %q, want the marked body untouched after a 304", got)
	}
	requests := server.Requests()
	if len(requests) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(requests))
	}
	if requests[1].Status != 304 {
		t.Fatalf("second request status = %d, want 304", requests[1].Status)
	}
	if requests[1].Bytes != 0 {
		t.Fatalf("second request transferred %d bytes, want 0", requests[1].Bytes)
	}
}

func TestHelperReprovisionsWhenUpstreamMoved(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	_, _, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("cold run exit = %d, want 0", code)
	}
	firstState := readState(t, dir)

	moved := helperFiles()
	moved["go.mk"] = "# go.mk v2\n"
	server.SetFiles(moved)

	_, stderr, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("moved run exit = %d, want 0: %s", code, stderr)
	}

	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v2\n" {
		t.Fatalf("go.mk = %q, want the advanced body", got)
	}
	secondState := readState(t, dir)
	if secondState["etag"] == firstState["etag"] {
		t.Fatal("state etag did not change after upstream moved")
	}
}

func readState(t *testing.T, dir string) map[string]string {
	t.Helper()
	file, err := os.Open(filepath.Join(dir, ".make", ".go-mk-fetch-state"))
	if err != nil {
		t.Fatalf("open fetch state: %v", err)
	}
	defer func() { _ = file.Close() }()
	state := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if found {
			state[key] = value
		}
	}
	return state
}

func writeState(t *testing.T, dir string, ref string, etag string, timestamp int64) {
	t.Helper()
	body := fmt.Sprintf("ref=%s\netag=%s\ntimestamp=%d\n", ref, etag, timestamp)
	path := filepath.Join(dir, ".make", ".go-mk-fetch-state")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for state: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/go-mk/ -run TestHelper -v`
Expected: FAIL on `TestHelperColdProvisionRecordsState` with `open fetch state: no such file or directory`, and on the 304 test because the helper re-downloads and overwrites the marked `go.mk`.

- [ ] **Step 3: Write minimal implementation**

In `scripts/go-mk-bootstrap.sh`, add these constants after `FETCH_MAX_TIME`:

```bash
STATE_PATH="${MAKE_DIR}/.go-mk-fetch-state"
VALIDATION_CONNECT_TIMEOUT=2
VALIDATION_MAX_TIME=3
```

Add the state and validation functions above `provision`:

```bash
current_epoch_seconds() {
    # EPOCHSECONDS is a bash 5 builtin, so the common path spawns no process.
    if [[ -n "${EPOCHSECONDS:-}" ]]; then
        printf '%s' "${EPOCHSECONDS}"
        return 0
    fi
    date +%s
}

read_state_field() {
    local field_name="$1"
    local line
    if [[ ! -s "${STATE_PATH}" ]]; then
        return 1
    fi
    while IFS= read -r line; do
        if [[ "${line}" == "${field_name}="* ]]; then
            printf '%s' "${line#"${field_name}="}"
            return 0
        fi
    done < "${STATE_PATH}"
    return 1
}

write_state() {
    local etag_value="$1"
    {
        printf 'ref=%s\n' "${GO_MK_API_REF}"
        printf 'etag=%s\n' "${etag_value}"
        printf 'timestamp=%s\n' "$(current_epoch_seconds)"
    } > "${STATE_PATH}"
}

# validate_upstream sends one conditional request. It prints the HTTP status
# code and returns 1 only when the request never completed, so an unreachable
# upstream stays distinguishable from a served 304.
validate_upstream() {
    local destination_path="$1"
    local known_etag="$2"
    local status_code
    local -a header_args=()
    if [[ -n "${known_etag}" ]]; then
        header_args=(-H "If-None-Match: ${known_etag}")
    fi
    if ! status_code=$(curl -sS \
        --connect-timeout "${VALIDATION_CONNECT_TIMEOUT}" \
        --max-time "${VALIDATION_MAX_TIME}" \
        "${header_args[@]}" \
        -o "${destination_path}" -w '%{http_code}' \
        "${GO_MK_CODELOAD_BASE}/${GO_MK_API_REPO}/tar.gz/${GO_MK_API_REF}" 2>/dev/null); then
        return 1
    fi
    printf '%s' "${status_code}"
}
```

Replace `provision` so it records the `ETag` from the same response that carried the body, using `-D` rather than a second request:

```bash
provision() {
    local stage_root
    local stage_dir
    local status_code
    local etag_value

    stage_root=$(mktemp -d "${TMPDIR:-/tmp}/go-mk-stage.XXXXXXXX") || return 1
    trap 'rm -rf "${stage_root}"' RETURN

    if ! status_code=$(curl -sS --connect-timeout 5 --max-time "${FETCH_MAX_TIME}" \
        --retry 3 --retry-delay 2 \
        -D "${stage_root}/headers" \
        -o "${stage_root}/snapshot.tar.gz" -w '%{http_code}' \
        "${GO_MK_CODELOAD_BASE}/${GO_MK_API_REPO}/tar.gz/${GO_MK_API_REF}" 2>/dev/null); then
        return 1
    fi
    if [[ "${status_code}" != "200" ]]; then
        return 1
    fi

    stage_dir="${stage_root}/tree"
    mkdir -p "${stage_dir}"
    if ! tar -xzf "${stage_root}/snapshot.tar.gz" -C "${stage_dir}" --strip-components 1 2>/dev/null; then
        return 1
    fi
    if ! assets_complete "${stage_dir}"; then
        return 1
    fi
    install_from_stage "${stage_dir}"
    etag_value=$(awk 'tolower($1) == "etag:" { print $2 }' "${stage_root}/headers" | tr -d '\r' | tail -n 1)
    write_state "${etag_value}"
    return 0
}
```

Replace the fetch branch in `main` with the validation decision, so the body reads:

```bash
main() {
    local known_etag=""
    local status_code=""
    local probe_root

    mkdir -p "${MAKE_DIR}"

    if [[ -n "${GO_MK_DEV_DIR}" ]]; then
        install_from_dev_dir
        return 0
    fi

    if [[ "${GO_MK_SKIP_FETCH:-}" == "1" ]]; then
        if assets_complete "${MAKE_DIR}"; then
            return 0
        fi
        printf '%s\n' "error: GO_MK_SKIP_FETCH=1 but .make is missing a required asset" >&2
        return 1
    fi

    if assets_complete "${MAKE_DIR}"; then
        known_etag=$(read_state_field "etag" || printf '')
    fi

    if [[ -n "${known_etag}" ]]; then
        probe_root=$(mktemp -d "${TMPDIR:-/tmp}/go-mk-probe.XXXXXXXX") || return 1
        status_code=$(validate_upstream "${probe_root}/snapshot.tar.gz" "${known_etag}" || printf '')
        rm -rf "${probe_root}"
        if [[ "${status_code}" == "304" ]]; then
            # Deliberately no state write. The reuse window is a fixed hour from
            # the last real download, not a window a successful check can slide
            # forward, so a 304 leaves both the assets and the state alone.
            return 0
        fi
    fi

    if provision; then
        return 0
    fi

    printf '%s\n' "error: could not provision go-makefile assets. Set GO_MK_DEV_DIR, or check network access to ${GO_MK_CODELOAD_BASE}" >&2
    return 1
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/go-mk/ -run TestHelper -v`
Expected: PASS, all five tests.

- [ ] **Step 5: Commit**

```bash
git add scripts/go-mk-bootstrap.sh cmd/go-mk/bootstrapfetch_test.go
git commit -S -m "Validate the engine tarball with a conditional request and record fetch state

Co-authored-by: Claude <noreply@anthropic.com>"
```

---

### Task 4: Bounded offline reuse

**Files:**
- Modify: `scripts/go-mk-bootstrap.sh`
- Modify: `cmd/go-mk/bootstrapfetch_test.go`

**Interfaces:**
- Consumes: `readState`, `writeState`, `runHelper`, `writeAsset`, `readAsset`, `helperFiles` from Tasks 2 and 3.
- Produces:
  - Shell functions `state_is_recent`, `format_age`, and `serve_from_disk_with_warning` inside the helper.
  - A warning on stderr containing the exact phrase `serving .make assets validated`.
  - `func warmMake(t *testing.T, dir string)` in `bootstrapfetch_test.go`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/go-mk/bootstrapfetch_test.go`.

```go
// warmMake populates .make with a complete asset set so only the validation
// decision is under test.
func warmMake(t *testing.T, dir string) {
	t.Helper()
	for name, body := range helperFiles() {
		if name == "scripts/go-mk-bootstrap.sh" {
			continue
		}
		writeAsset(t, dir, name, body)
	}
}

func TestHelperServesDiskWhenUpstreamTimesOutAndStateIsRecent(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(-10*time.Minute).Unix())
	server.Stall(5 * time.Second)

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0 when state is recent: %s", code, stderr)
	}
	if !strings.Contains(stderr, "serving .make assets validated") {
		t.Fatalf("stderr = %q, want a warning naming the served assets", stderr)
	}
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the warm body preserved", got)
	}
}

func TestHelperFailsWhenUpstreamTimesOutAndStateIsStale(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(-2*time.Hour).Unix())
	server.Stall(5 * time.Second)

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code == 0 {
		t.Fatal("helper exit = 0, want non-zero when state is older than the reuse window")
	}
	if strings.Contains(stderr, "serving .make assets validated") {
		t.Fatalf("stderr = %q, want no serve warning on the stale path", stderr)
	}
	// Even on the failing path, nothing may be destroyed.
	if got := readAsset(t, dir, "go.mk"); got != "# go.mk v1\n" {
		t.Fatalf("go.mk = %q, want the warm body preserved through a failure", got)
	}
}

func TestHelperTreatsFutureTimestampAsStale(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Add(2*time.Hour).Unix())
	server.Stall(5 * time.Second)

	_, _, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
	})
	if code == 0 {
		t.Fatal("helper exit = 0, want non-zero for a future timestamp")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/go-mk/ -run TestHelper -v`
Expected: FAIL on the recent-state test, because the helper currently falls through to `provision` and exits non-zero when the stalled server times out.

- [ ] **Step 3: Write minimal implementation**

Add the constant beside the other timeouts in `scripts/go-mk-bootstrap.sh`:

```bash
REUSE_WINDOW_SECONDS=3600
```

Add the reuse decision above `main`:

```bash
# state_is_recent reports whether the recorded validation is inside the reuse
# window. A timestamp in the future, which a backwards clock produces, is not
# recent, so a bad clock forces a real fetch instead of an unbounded serve.
state_is_recent() {
    local recorded
    local now
    local age
    if ! recorded=$(read_state_field "timestamp"); then
        return 1
    fi
    if [[ ! "${recorded}" =~ ^[0-9]+$ ]]; then
        return 1
    fi
    now=$(current_epoch_seconds)
    if (( recorded > now )); then
        return 1
    fi
    age=$(( now - recorded ))
    (( age <= REUSE_WINDOW_SECONDS ))
}

format_age() {
    local seconds="$1"
    if (( seconds < 60 )); then
        printf '%ds' "${seconds}"
        return 0
    fi
    printf '%dm' "$(( seconds / 60 ))"
}

serve_from_disk_with_warning() {
    local recorded
    local now
    local etag_value
    recorded=$(read_state_field "timestamp")
    etag_value=$(read_state_field "etag" || printf 'unknown')
    now=$(current_epoch_seconds)
    printf '%s\n' "go-makefile: upstream unreachable; serving .make assets validated $(format_age $(( now - recorded ))) ago (etag ${etag_value}). Set GO_MK_SKIP_FETCH=1 to silence, or check network access to ${GO_MK_CODELOAD_BASE}" >&2
}
```

In `main`, insert the reuse branch between the `304` check and the `provision` call:

```bash
    if [[ -n "${known_etag}" && -z "${status_code}" ]] && state_is_recent; then
        serve_from_disk_with_warning
        return 0
    fi
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/go-mk/ -run TestHelper -v`
Expected: PASS, all eight tests.

- [ ] **Step 5: Commit**

```bash
git add scripts/go-mk-bootstrap.sh cmd/go-mk/bootstrapfetch_test.go
git commit -S -m "Serve .make assets for one hour when upstream validation fails

Co-authored-by: Claude <noreply@anthropic.com>"
```

---

### Task 5: CI never serves disk

**Files:**
- Modify: `scripts/go-mk-bootstrap.sh`
- Modify: `cmd/go-mk/bootstrapfetch_test.go`

**Interfaces:**
- Consumes: everything from Tasks 2 through 4.
- Produces: the shell function `running_in_ci` inside the helper.

- [ ] **Step 1: Write the failing test**

Append to `cmd/go-mk/bootstrapfetch_test.go`.

```go
func TestHelperAlwaysProvisionsUnconditionallyInCI(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()
	warmMake(t, dir)
	// Fresh state that would produce a 304 and a disk serve off CI.
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Unix())

	_, stderr, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GITHUB_ACTIONS":      "true",
		"GITHUB_RUN_ID":       "1",
	})
	if code != 0 {
		t.Fatalf("helper exit = %d, want 0: %s", code, stderr)
	}

	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(requests))
	}
	if requests[0].IfNoneMatch != "" {
		t.Fatalf("CI request carried If-None-Match %q, want none", requests[0].IfNoneMatch)
	}
	if requests[0].Status != 200 {
		t.Fatalf("CI request status = %d, want 200", requests[0].Status)
	}
}

func TestHelperFailsInCIWhenUpstreamIsUnreachableDespiteFreshState(t *testing.T) {
	dir := t.TempDir()
	warmMake(t, dir)
	writeState(t, dir, "main", `"cached-etag"`, time.Now().Unix())

	_, _, code := runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": "http://127.0.0.1:9",
		"GITHUB_ACTIONS":      "true",
		"GITHUB_RUN_ID":       "1",
	})
	if code == 0 {
		t.Fatal("helper exit = 0 in CI with an unreachable upstream, want non-zero")
	}
}

func TestHelperTreatsGithubActionsWithoutRunIdAsLocal(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	dir := t.TempDir()

	_, _, code := runHelper(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("cold run exit = %d, want 0", code)
	}

	_, _, code = runHelper(t, dir, map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GITHUB_ACTIONS":      "true",
		// No GITHUB_RUN_ID, so this is not a CI run.
	})
	if code != 0 {
		t.Fatalf("second run exit = %d, want 0", code)
	}

	requests := server.Requests()
	if requests[len(requests)-1].Status != 304 {
		t.Fatalf("last status = %d, want 304 because GITHUB_ACTIONS alone is not CI",
			requests[len(requests)-1].Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/go-mk/ -run TestHelper -v`
Expected: FAIL on `TestHelperAlwaysProvisionsUnconditionallyInCI`, because the helper sends `If-None-Match` and gets a `304`.

- [ ] **Step 3: Write minimal implementation**

Add above `main` in `scripts/go-mk-bootstrap.sh`:

```bash
# running_in_ci matches the test the engine already uses for a real GitHub
# Actions run. GITHUB_ACTIONS alone is not a CI run, so a local shell that
# happens to export it still gets the local behavior.
running_in_ci() {
    [[ "${GITHUB_ACTIONS:-}" == "true" && -n "${GITHUB_RUN_ID:-}" ]]
}
```

In `main`, guard the state read so CI never validates conditionally:

```bash
    if ! running_in_ci && assets_complete "${MAKE_DIR}"; then
        known_etag=$(read_state_field "etag" || printf '')
    fi
```

and guard the reuse branch so CI never serves disk:

```bash
    if ! running_in_ci && [[ -n "${known_etag}" && -z "${status_code}" ]] && state_is_recent; then
        serve_from_disk_with_warning
        return 0
    fi
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/go-mk/ -v`
Expected: PASS, every test in the package.

- [ ] **Step 5: Commit**

```bash
git add scripts/go-mk-bootstrap.sh cmd/go-mk/bootstrapfetch_test.go
git commit -S -m "Provision unconditionally under GitHub Actions

Co-authored-by: Claude <noreply@anthropic.com>"
```

---

### Task 6: Delegate from bootstrap.mk

The consumer-facing change, and the one that ends the destructive offline parse.

**Files:**
- Modify: `bootstrap.mk`
- Modify: `cmd/go-mk/bootstrap_assets/bootstrap.mk`
- Create: `cmd/go-mk/bootstrapparse_test.go`

**Interfaces:**
- Consumes: the helper contract from Tasks 2 through 5, and `readAsset`, `repoRootForTest`, `asExitError`, `helperFiles` from Task 2.
- Produces:
  - `bootstrap.mk` sets `GO_MK_BOOTSTRAP_FETCHED := 1` and `GO_MK_PROVISION`, runs `.make/scripts/go-mk-bootstrap.sh`, and includes `.make/go.mk`.
  - `func newConsumer(t *testing.T) string` in `bootstrapparse_test.go`.
  - `func runMake(t *testing.T, dir string, env map[string]string) (output string, exitCode int)` in `bootstrapparse_test.go`.
  - `func consumerFiles() map[string]string` in `bootstrapparse_test.go`.

- [ ] **Step 1: Write the failing test**

Create `cmd/go-mk/bootstrapparse_test.go`.

```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newConsumer builds a temp repo shaped like a real consumer: its own Makefile
// that includes the committed bootstrap.mk.
func newConsumer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(repoRootForTest(t), "bootstrap.mk")
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read bootstrap.mk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bootstrap.mk"), body, 0o644); err != nil {
		t.Fatalf("write bootstrap.mk: %v", err)
	}
	makefile := "BINARY := probe\nCMD := ./cmd/probe\ninclude bootstrap.mk\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	return dir
}

// runMake parses the consumer Makefile without running any recipe, which is
// enough to exercise the whole parse-time fetch path.
func runMake(t *testing.T, dir string, env map[string]string) (string, int) {
	t.Helper()
	command := exec.Command("make", "-n", "help")
	command.Dir = dir
	command.Env = append(os.Environ(), "GITHUB_ACTIONS=", "GITHUB_RUN_ID=", "GO_MK_DEV_DIR=")
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	var combined strings.Builder
	command.Stdout = &combined
	command.Stderr = &combined
	err := command.Run()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			t.Fatalf("run make: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return combined.String(), code
}

// consumerFiles is the served tree for a parse test. go.mk is a stub with a
// help target, so the parse succeeds without pulling the real engine.
func consumerFiles() map[string]string {
	files := helperFiles()
	files["go.mk"] = "help:\n\t@printf 'stub help\\n'\n"
	return files
}

func TestOfflineParseDoesNotDestroyCachedAssets(t *testing.T) {
	server := newFetchServer(t, consumerFiles())
	dir := newConsumer(t)

	output, code := runMake(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("warm parse exit = %d, want 0: %s", code, output)
	}
	before := readAsset(t, dir, "go.mk")
	if before == "" {
		t.Fatal("go.mk absent after the first parse")
	}

	// Upstream disappears. The parse may fail, but it must not remove assets.
	_, _ = runMake(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": "http://127.0.0.1:9"})

	if after := readAsset(t, dir, "go.mk"); after != before {
		t.Fatalf("go.mk = %q after an offline parse, want the cached body %q preserved", after, before)
	}
	if readAsset(t, dir, "golangci.yml") == "" {
		t.Fatal("golangci.yml was destroyed by an offline parse")
	}
}

func TestWarmParseIssuesOneRequestTotal(t *testing.T) {
	server := newFetchServer(t, consumerFiles())
	dir := newConsumer(t)

	if output, code := runMake(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()}); code != 0 {
		t.Fatalf("cold parse exit = %d: %s", code, output)
	}
	coldRequests := len(server.Requests())

	if output, code := runMake(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()}); code != 0 {
		t.Fatalf("warm parse exit = %d: %s", code, output)
	}

	warmRequests := server.Requests()[coldRequests:]
	if len(warmRequests) != 1 {
		t.Fatalf("warm parse issued %d requests, want exactly 1", len(warmRequests))
	}
	if warmRequests[0].Status != 304 {
		t.Fatalf("warm request status = %d, want 304", warmRequests[0].Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/go-mk/ -run "TestOfflineParse|TestWarmParse" -v`

Expected: FAIL. `TestOfflineParseDoesNotDestroyCachedAssets` fails because today's `_go_mk_prime` deletes each asset before downloading, which is the reproduction this task fixes. `TestWarmParseIssuesOneRequestTotal` fails because today's `bootstrap.mk` downloads unconditionally.

- [ ] **Step 3: Write minimal implementation**

Replace `bootstrap.mk` entirely.

```makefile
# bootstrap.mk: tiny shim that obtains the go-makefile bootstrap helper, runs
# it, and includes the engine. Consumer Makefiles set their identity vars
# (BINARY, CMD, VPKG, MODULES, etc.) then `include bootstrap.mk`.
#
# This file is canonical in agoodkind/go-makefile. Consumers commit a copy.
# It deliberately holds no fetch policy beyond obtaining the helper: the helper
# is fetched, so a change to validation, reuse, or failure behavior reaches
# every consumer on its next run with no consumer-side change.

GO_MK_DEV_DIR  ?=
GO_MK_MODULES  ?=
GO_MK          := .make/go.mk
GO_MK_BASE_URL ?= https://raw.githubusercontent.com/agoodkind/go-makefile/main
GO_MK_API_REPO ?= agoodkind/go-makefile
GO_MK_API_REF  ?= main

GO_MK_BOOTSTRAP := .make/scripts/go-mk-bootstrap.sh
# The helper URL follows GO_MK_API_REF so a ref-pinned consumer gets that ref's
# helper. GO_MK_BASE_URL ends in /main and would pin the helper to main, so it
# is not used here.
GO_MK_BOOTSTRAP_URL := https://raw.githubusercontent.com/$(GO_MK_API_REPO)/$(GO_MK_API_REF)/scripts/go-mk-bootstrap.sh

# Obtaining the helper is the only fetch rule left in consumer-committed code.
# It never removes an existing helper, so a warm checkout stays usable when the
# network is gone, and only a cold offline start fails here.
define _go_mk_get_bootstrap
	if [ -n "$(GO_MK_DEV_DIR)" ] && [ -f "$(GO_MK_DEV_DIR)/scripts/go-mk-bootstrap.sh" ]; then \
		mkdir -p .make/scripts; \
		cp "$(GO_MK_DEV_DIR)/scripts/go-mk-bootstrap.sh" "$(GO_MK_BOOTSTRAP)"; \
	elif [ -s "$(GO_MK_BOOTSTRAP)" ]; then \
		: ; \
	else \
		mkdir -p .make/scripts; \
		tmp=$$(mktemp "$(GO_MK_BOOTSTRAP).tmp.XXXXXX") || exit 1; \
		if curl -fsSL --connect-timeout 5 --max-time 10 --retry 3 --retry-delay 2 \
			"$(GO_MK_BOOTSTRAP_URL)" -o "$$tmp" 2>/dev/null && [ -s "$$tmp" ]; then \
			mv "$$tmp" "$(GO_MK_BOOTSTRAP)"; \
		else \
			rm -f "$$tmp"; \
			printf '%s\n' "error: could not obtain $(GO_MK_BOOTSTRAP). Set GO_MK_DEV_DIR, or check network access to raw.githubusercontent.com" >&2; \
			exit 1; \
		fi; \
	fi; \
	chmod +x "$(GO_MK_BOOTSTRAP)"
endef

GO_MK_BOOTSTRAP_FETCHED := 1

ifeq ($(strip $(GO_MK_SKIP_FETCH)),1)
$(if $(wildcard $(GO_MK_BOOTSTRAP)),,$(error go-makefile expected $(GO_MK_BOOTSTRAP); rerun without GO_MK_SKIP_FETCH))
else
$(shell { $(call _go_mk_get_bootstrap); } 1>&2)
endif

# The helper provisions every asset and owns the validation, reuse, and failure
# rules. A non-zero exit means it could not produce a usable .make, so stop
# rather than parse an engine that is not there.
GO_MK_PROVISION := $(shell GO_MK_MODULES="$(GO_MK_MODULES)" bash "$(GO_MK_BOOTSTRAP)" >&2 && printf ok)
$(if $(filter ok,$(GO_MK_PROVISION)),,$(error go-makefile failed to provision its assets))

# go.mk handles -including the modules at its tail (after all its variables
# are defined), so the modules see build-check etc. Don't duplicate
# the include here or every module target gets overriding-commands warnings.
-include $(GO_MK)
```

Copy it to the embedded asset so `reconcileBootstrapMk` writes the same file:

```bash
cp bootstrap.mk cmd/go-mk/bootstrap_assets/bootstrap.mk
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/go-mk/ -run "TestOfflineParse|TestWarmParse" -v`
Expected: PASS, both tests.

Then confirm the embedded copy matches. Run: `diff bootstrap.mk cmd/go-mk/bootstrap_assets/bootstrap.mk`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add bootstrap.mk cmd/go-mk/bootstrap_assets/bootstrap.mk cmd/go-mk/bootstrapparse_test.go
git commit -S -m "Delegate fetch policy from bootstrap.mk to go-mk-bootstrap.sh

Co-authored-by: Claude <noreply@anthropic.com>"
```

---

### Task 7: Retire go.mk's duplicate provisioning and the dead variables

**Files:**
- Modify: `go.mk:10-15`, `go.mk:52-54`
- Modify: `scripts/go-mk-fetch-one.sh:57`
- Modify: `cmd/go-mk/bootstrapparse_test.go`

**Interfaces:**
- Consumes: `GO_MK_PROVISION` set by `bootstrap.mk` in Task 6, and `repoRootForTest` from Task 2.
- Produces: `go.mk` skips its own prime when the helper already provisioned this run.

- [ ] **Step 1: Write the failing test**

Append to `cmd/go-mk/bootstrapparse_test.go`.

```go
func TestDeadCacheVariablesAreGone(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRootForTest(t), "go.mk"))
	if err != nil {
		t.Fatalf("read go.mk: %v", err)
	}
	for _, name := range []string{"GO_MK_URL", "GO_MK_CACHE_DIR", "GO_MK_CACHE "} {
		if strings.Contains(string(body), name) {
			t.Fatalf("go.mk still defines the unused variable %s", strings.TrimSpace(name))
		}
	}
}

func TestMoratoriumWordingIsGone(t *testing.T) {
	root := repoRootForTest(t)
	for _, relative := range []string{
		"bootstrap.mk",
		"cmd/go-mk/bootstrap_assets/bootstrap.mk",
		"scripts/go-mk-fetch-one.sh",
	} {
		body, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		if strings.Contains(strings.ToLower(string(body)), "moratorium") {
			t.Fatalf("%s still references the moratorium", relative)
		}
	}
}

// TestOldBootstrapStillParsesWithNewGoMk covers the rollout window, where a
// consumer has merged nothing yet but already fetches the new go.mk. Its
// bootstrap.mk never sets GO_MK_PROVISION, so go.mk must fall back to priming
// the assets itself rather than assuming the helper ran.
func TestOldBootstrapStillParsesWithNewGoMk(t *testing.T) {
	files := consumerFiles()
	// Serve the real go.mk so its own provisioning path is what runs.
	realGoMk, err := os.ReadFile(filepath.Join(repoRootForTest(t), "go.mk"))
	if err != nil {
		t.Fatalf("read go.mk: %v", err)
	}
	files["go.mk"] = string(realGoMk)
	server := newFetchServer(t, files)

	dir := t.TempDir()
	// A bootstrap.mk shaped like the pre-helper one: it fetches go.mk itself
	// and sets no GO_MK_PROVISION.
	oldBootstrap := `GO_MK := .make/go.mk
GO_MK_API_REPO ?= agoodkind/go-makefile
GO_MK_API_REF  ?= main
GO_MK_BOOTSTRAP_FETCHED := 1
$(shell mkdir -p .make && curl -sS -o .make/snapshot.tar.gz "$(GO_MK_CODELOAD_BASE)/$(GO_MK_API_REPO)/tar.gz/$(GO_MK_API_REF)" && tar -xzf .make/snapshot.tar.gz -C .make --strip-components 1 2>/dev/null)
-include $(GO_MK)
`
	if err := os.WriteFile(filepath.Join(dir, "bootstrap.mk"), []byte(oldBootstrap), 0o644); err != nil {
		t.Fatalf("write old bootstrap.mk: %v", err)
	}
	makefile := "BINARY := probe\ninclude bootstrap.mk\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	output, code := runMake(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("mixed-version parse exit = %d, want 0: %s", code, output)
	}
	if readAsset(t, dir, "go.mk") == "" {
		t.Fatal("go.mk absent after a mixed-version parse")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/go-mk/ -run "TestDeadCache|TestMoratorium|TestOldBootstrap" -v`
Expected: FAIL on `TestDeadCacheVariablesAreGone` and `TestMoratoriumWordingIsGone`, since `go.mk:10,11,15` still define the variables and `scripts/go-mk-fetch-one.sh:57` still says "no cache fallback". `TestOldBootstrapStillParsesWithNewGoMk` should already pass, and it is here to stay green through the change, so treat a failure of that one as a regression rather than as the red step.

- [ ] **Step 3: Write minimal implementation**

In `go.mk`, delete lines 10, 11, and 15, leaving this block:

```makefile
GO_MK_BASE_URL  ?= https://raw.githubusercontent.com/agoodkind/go-makefile/main
GO_MK_API_REPO  ?= agoodkind/go-makefile
GO_MK_API_REF   ?= main
```

Guard `go.mk`'s prime so it runs only for a consumer whose `bootstrap.mk` predates the helper. Replace lines 52-54:

```makefile
# The bootstrap helper provisions every asset in one extraction, so this prime
# is only for a consumer whose committed bootstrap.mk predates the helper. It
# is removed once the fleet has migrated.
ifneq ($(strip $(GO_MK_SKIP_FETCH)),1)
ifeq ($(strip $(GO_MK_PROVISION)),)
$(shell mkdir -p .make && { $(call _go_mk_prime); } 1>&2)
endif
endif
```

In `scripts/go-mk-fetch-one.sh`, replace the closing message:

```bash
printf "error: %s fetch failed. Set GO_MK_DEV_DIR or check network access to %s\n" \
    "${relative_path}" "${base_url}"
exit 1
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/go-mk/ -v`
Expected: PASS, every test.

- [ ] **Step 5: Commit**

```bash
git add go.mk scripts/go-mk-fetch-one.sh cmd/go-mk/bootstrapparse_test.go
git commit -S -m "Remove the duplicate prime, the dead cache variables, and the moratorium wording

Co-authored-by: Claude <noreply@anthropic.com>"
```

---

### Task 8: Document the fetch contract

**Files:**
- Create: `docs/fetch.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the behavior built in Tasks 2 through 7.
- Produces: nothing code depends on.

- [ ] **Step 1: Write the document**

Create `docs/fetch.md`.

```markdown
# Consumer fetch

A consumer commits one file, `bootstrap.mk`, and fetches everything else. The
committed file obtains a helper script and runs it; the helper provisions
`go.mk`, the shared `golangci.yml`, the helper scripts, `notices.txt`, and the
selected `GO_MK_MODULES` into `.make/`.

Fetch policy lives in the helper rather than in `bootstrap.mk`, so a change to
how assets are validated or reused reaches every consumer on its next run
without a consumer change.

## Upstream is consulted on every parse

The helper sends one conditional request for the engine tarball, carrying the
`ETag` it recorded last time. A `304` means the tree is byte-identical to
upstream, so the files already under `.make/` are correct and nothing is
transferred. A `200` carries the new tree.

The `ETag` is a content hash, so it answers whether the assets changed rather
than whether a commit happened. A commit that leaves the tree identical keeps
the same `ETag`, and reusing the local copy then is correct.

## Provisioning never destroys

The helper extracts into a staging directory and verifies every required asset
there before replacing anything under `.make/`. A failed or partial download
leaves the previous assets in place, so a parse without a network keeps a warm
checkout usable.

## Reuse after a failed validation is bounded

When the conditional request does not complete, the helper serves the local
assets if the last successful validation was within the past hour, and prints
one warning naming the recorded `ETag` and its age. Past that window it forces
a real fetch and fails loudly rather than running on assets it can no longer
vouch for.

## CI always fetches

Under a real GitHub Actions run, meaning `GITHUB_ACTIONS` is `true` and
`GITHUB_RUN_ID` is set, the helper ignores the recorded state, sends no
conditional request, and provisions unconditionally. A CI result therefore never
rests on a local copy that upstream did not confirm.

## Offline and pinned work

`GO_MK_SKIP_FETCH=1` skips the network entirely and requires the assets to be
present already. `GO_MK_DEV_DIR` points at a local engine checkout and takes
precedence over everything else, which is how an engine change is tested in a
consumer before it is pushed.
```

- [ ] **Step 2: Link it from the README**

Add a line to the README's documentation list pointing at `docs/fetch.md`, described as how a consumer obtains the engine and when it reuses what it already has.

- [ ] **Step 3: Verify the claims against the code**

Run: `go test ./cmd/go-mk/ -v`
Expected: PASS. Then reread `docs/fetch.md` beside `scripts/go-mk-bootstrap.sh` and confirm each stated behavior has a test backing it.

- [ ] **Step 4: Commit**

```bash
git add docs/fetch.md README.md
git commit -S -m "Document the consumer fetch contract

Co-authored-by: Claude <noreply@anthropic.com>"
```

---

## Rollout after the plan completes

1. Merge. Consumers pick up the helper, the `go.mk` changes, and the dead-variable cleanup on their next run with no pull request, because those files are fetched.
2. Run the consumer update so each repo takes the new `bootstrap.mk`, then review and merge those pull requests. This is the round that ends the destructive offline parse.
3. Later, once every consumer has migrated, remove `_go_mk_prime`, `go_mk_fetch_bootstrap`, and `go-mk-fetch-one` from `go.mk`, along with `scripts/go-mk-fetch-one.sh`.
