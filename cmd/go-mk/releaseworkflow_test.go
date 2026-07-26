package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWorkflowConfiguresDarwinCcache(t *testing.T) {
	workflow := readBuildWorkflow(t)

	ensure := buildWorkflowStep(t, workflow, "Ensure ccache in darwin cross container")
	requireWorkflowContains(t, ensure, "if: matrix.container != '' && inputs.cgo")
	requireWorkflowContains(t, ensure, "apt-get install -y --no-install-recommends ccache")
	requireWorkflowContains(t, ensure, "ccache --version")

	restore := buildWorkflowStep(t, workflow, "Restore darwin ccache")
	requireWorkflowContains(t, restore, "id: darwin-ccache-restore")
	requireWorkflowContains(t, restore, "uses: actions/cache/restore@v6")
	requireWorkflowContains(t, restore, "path: ~/.ccache")
	requireWorkflowContains(t, restore, darwinCcacheWorkflowKey())
	requireWorkflowContains(t, restore, "ccache-${{ runner.os }}-${{ runner.arch }}-${{ github.repository_id }}-release-${{ matrix.goos }}-${{ matrix.goarch }}-goreleaser-cross-v1.26.3-${{ matrix.cc }}-")

	configure := buildWorkflowStep(t, workflow, "Configure darwin cross compilers")
	requireWorkflowContains(t, configure, `echo "CCACHE_DIR=${HOME}/.ccache"`)
	requireWorkflowContains(t, configure, `echo "CCACHE_BASEDIR=${GITHUB_WORKSPACE}"`)
	requireWorkflowContains(t, configure, `echo "CCACHE_COMPILERCHECK=content"`)
	requireWorkflowContains(t, configure, `echo "CCACHE_NOHASHDIR=true"`)
	requireWorkflowContains(t, configure, `mkdir -p "${HOME}/.ccache"`)
	// ccache interposes through masquerade symlinks so GO_MK_CC stays a single
	// word; consumer dep recipes may invoke "$CC" as one word and a two-word
	// value fails there with exit 127.
	requireWorkflowContains(t, configure, `ln -sf "${ccache_bin}" "${wrapper_dir}/${{ matrix.cc }}"`)
	requireWorkflowContains(t, configure, `ln -sf "${ccache_bin}" "${wrapper_dir}/${{ matrix.cxx }}"`)
	requireWorkflowContains(t, configure, `echo "${wrapper_dir}" >> "${GITHUB_PATH}"`)
	requireWorkflowContains(t, configure, `echo "GO_MK_CC=${{ matrix.cc }}"`)
	requireWorkflowContains(t, configure, `echo "GO_MK_CXX=${{ matrix.cxx }}"`)
	requireWorkflowContains(t, configure, "ccache --zero-stats")

	show := buildWorkflowStep(t, workflow, "Show ccache stats")
	requireWorkflowContains(t, show, "if: inputs.cgo")

	save := buildWorkflowStep(t, workflow, "Save darwin ccache")
	requireWorkflowContains(t, save, "if: matrix.cc != '' && inputs.cgo && steps.darwin-ccache-restore.outputs.cache-hit != 'true'")
	requireWorkflowContains(t, save, "uses: actions/cache/save@v6")
	requireWorkflowContains(t, save, "path: ~/.ccache")
	requireWorkflowContains(t, save, darwinCcacheWorkflowKey())

	requireWorkflowOrder(t, workflow, "      - name: Configure darwin cross compilers", "      - name: Build go-mk cache manifest")
	// Match the "Compile" step exactly with a trailing newline, so the assertion
	// does not accidentally anchor on another step name.
	requireWorkflowOrder(t, workflow, "      - name: Compile\n", "      - name: Save darwin ccache")
}

// TestBuildWorkflowCcacheKeysAreReusable proves no compiler-cache key carries a
// value unique to one run. Such a key can never be hit, so the save guarded on a
// miss runs every time and each run leaves an entry nothing will read again. A
// repository shares one cache budget, and eviction removes whatever was touched
// least recently rather than whatever is useless, so those write-once entries
// push out the caches other jobs restore.
func TestBuildWorkflowCcacheKeysAreReusable(t *testing.T) {
	workflow := readBuildWorkflow(t)

	for _, runScoped := range []string{"github.run_id", "github.run_attempt", "github.run_number", "github.sha"} {
		for _, line := range strings.Split(workflow, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "key: ccache-") && !strings.HasPrefix(trimmed, "ccache-") {
				continue
			}
			if strings.Contains(trimmed, runScoped) {
				t.Errorf("compiler-cache key carries %s, so it can never be hit:\n%s", runScoped, trimmed)
			}
		}
	}
}

// TestBuildWorkflowCcacheKeysTrackSubmodulePins proves every compiler-cache key
// carries the submodule fingerprint. A submodule holding C or C++ code is pinned
// by a commit recorded in the index, so bumping it changes what compiles while
// every tracked file stays byte-identical. Without the fingerprint the key still
// matches, the save is skipped, and the cache keeps objects for sources that no
// longer exist.
func TestBuildWorkflowCcacheKeysTrackSubmodulePins(t *testing.T) {
	workflow := readBuildWorkflow(t)

	fingerprint := buildWorkflowStep(t, workflow, "Fingerprint submodule pins")
	requireWorkflowContains(t, fingerprint, "id: submodule-pins")
	requireWorkflowContains(t, fingerprint, "git submodule status --recursive")

	keyCount := 0
	for _, line := range strings.Split(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "key: ccache-") {
			continue
		}
		keyCount++
		if !strings.Contains(trimmed, "steps.submodule-pins.outputs.digest") {
			t.Errorf("compiler-cache key does not track submodule pins:\n%s", trimmed)
		}
	}
	if keyCount == 0 {
		t.Fatal("workflow declares no compiler-cache key")
	}
}

func readBuildWorkflow(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", ".github", "workflows", "_build.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func buildWorkflowStep(t *testing.T, workflow string, name string) string {
	t.Helper()
	marker := "      - name: " + name
	start := strings.Index(workflow, marker)
	if start < 0 {
		t.Fatalf("workflow missing step %q", name)
	}
	remainder := workflow[start+len(marker):]
	next := strings.Index(remainder, "\n      - ")
	if next < 0 {
		return workflow[start:]
	}
	return workflow[start : start+len(marker)+next]
}

func requireWorkflowContains(t *testing.T, text string, snippet string) {
	t.Helper()
	if !strings.Contains(text, snippet) {
		t.Fatalf("workflow snippet missing:\n%s\n\nin:\n%s", snippet, text)
	}
}

func requireWorkflowOrder(t *testing.T, workflow string, before string, after string) {
	t.Helper()
	beforeIndex := strings.Index(workflow, before)
	if beforeIndex < 0 {
		t.Fatalf("workflow missing %q", before)
	}
	afterIndex := strings.Index(workflow, after)
	if afterIndex < 0 {
		t.Fatalf("workflow missing %q", after)
	}
	if beforeIndex >= afterIndex {
		t.Fatalf("workflow has %q after %q", before, after)
	}
}

func darwinCcacheWorkflowKey() string {
	return "ccache-${{ runner.os }}-${{ runner.arch }}-${{ github.repository_id }}-release-${{ matrix.goos }}-${{ matrix.goarch }}-goreleaser-cross-v1.26.3-${{ matrix.cc }}-${{ hashFiles('go.mod', 'go.sum', 'go.work', 'go.work.sum') }}-${{ steps.submodule-pins.outputs.digest }}"
}
