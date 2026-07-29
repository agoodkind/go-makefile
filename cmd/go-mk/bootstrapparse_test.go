package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newConsumer builds a temp repo shaped like a real consumer: its own Makefile
// that includes the committed bootstrap.mk. It also pre-seeds
// .make/scripts/go-mk-bootstrap.sh with this checkout's own copy of the
// helper, so bootstrap.mk's own acquisition step (_go_mk_get_bootstrap)
// finds it already on disk and reuses it rather than fetching
// raw.githubusercontent.com/agoodkind/go-makefile/main/scripts/go-mk-bootstrap.sh:
// that URL reflects whatever is on the real main branch, not this
// worktree's uncommitted or unmerged changes to the script, so relying on
// it would make the test depend on push/merge state instead of being
// hermetic. This mirrors a real, common case too: a machine that has
// already run make once has the helper cached exactly this way.
func newConsumer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repoRoot := repoRootForTest(t)

	source := filepath.Join(repoRoot, "bootstrap.mk")
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read bootstrap.mk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bootstrap.mk"), body, 0o644); err != nil {
		t.Fatalf("write bootstrap.mk: %v", err)
	}

	helperSource := filepath.Join(repoRoot, "scripts", "go-mk-bootstrap.sh")
	helperBody, err := os.ReadFile(helperSource)
	if err != nil {
		t.Fatalf("read scripts/go-mk-bootstrap.sh: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".make", "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir .make/scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".make", "scripts", "go-mk-bootstrap.sh"), helperBody, 0o755); err != nil {
		t.Fatalf("seed .make/scripts/go-mk-bootstrap.sh: %v", err)
	}

	makefile := "BINARY := probe\nCMD := ./cmd/probe\ninclude bootstrap.mk\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	return dir
}

// runMake parses the consumer Makefile without running any recipe, which is
// enough to exercise the whole parse-time fetch path. Variables in env are set
// in the process environment, which GNU Make auto-exports to $(shell ...).
// That is only one of the three ways a consumer can set a GO_MK_* variable,
// and it is the one that happens to work even when bootstrap.mk forgets to
// forward it, so use runMakeWithCommandLineVars for the other two.
func runMake(t *testing.T, dir string, env map[string]string) (string, int) {
	t.Helper()
	command := exec.Command("make", "-n", "help")
	command.Dir = dir
	command.Env = testProcessEnvironment(env)
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

// runMakeWithCommandLineVars passes its variables as `make VAR=value`
// arguments rather than through the environment. GNU Make sets the Make
// variable for a command-line assignment but does NOT export it, so anything
// bootstrap.mk fails to forward explicitly reaches the helper as unset. A
// consumer writing `VAR := value` in their own Makefile before the include
// hits the same path, so this covers both of the two origins that runMake
// cannot reach.
func runMakeWithCommandLineVars(t *testing.T, dir string, vars map[string]string) (string, int) {
	t.Helper()
	arguments := []string{"-n", "help"}
	for key, value := range vars {
		arguments = append(arguments, key+"="+value)
	}
	command := exec.Command("make", arguments...)
	command.Dir = dir
	command.Env = testProcessEnvironment(nil)
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

// devDirTree writes a complete set of required assets to a directory, so it
// can stand in for a developer's own go-makefile checkout. go.mk prints a
// marker the test can look for, which is how a caller tells "the dev tree was
// used" apart from "upstream was downloaded over it".
func devDirTree(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir dev scripts: %v", err)
	}
	for name, body := range helperFiles() {
		if name == "go.mk" {
			body = "help:\n\t@printf '" + marker + "\\n'\n"
		}
		if name == "scripts/go-mk-bootstrap.sh" {
			helperBody, err := os.ReadFile(
				filepath.Join(repoRootForTest(t), "scripts", "go-mk-bootstrap.sh"))
			if err != nil {
				t.Fatalf("read helper: %v", err)
			}
			body = string(helperBody)
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestDevDirHonoredWhenSetOnTheMakeCommandLine covers the origin the rest of
// the suite cannot reach. bootstrap.mk decides what to do from GO_MK_DEV_DIR
// and then runs the helper, which owns every asset install; if the variable is
// not forwarded explicitly the two disagree. With an unreachable upstream the
// symptom is an error telling the user to set the variable they just set, and
// with a reachable one it is worse and silent: the helper downloads upstream
// over the developer's own checkout, so they build and lint against main while
// believing they are testing local edits.
func TestDevDirHonoredWhenSetOnTheMakeCommandLine(t *testing.T) {
	dir := newConsumer(t)
	devDir := devDirTree(t, "from-dev-dir")

	output, code := runMakeWithCommandLineVars(t, dir, map[string]string{
		"GO_MK_DEV_DIR":       devDir,
		"GO_MK_CODELOAD_BASE": unreachableCodeloadBase(t),
	})
	if code != 0 {
		t.Fatalf("parse exit = %d with GO_MK_DEV_DIR set on the command line, want 0: %s", code, output)
	}
	if !strings.Contains(output, "from-dev-dir") {
		t.Fatalf("parse output = %q, want the dev tree's go.mk (marker \"from-dev-dir\"); "+
			"the helper used upstream instead of the developer's checkout", output)
	}
}

// TestSkipFetchHonoredWhenSetOnTheMakeCommandLine is the same split for the
// flag that means "do not touch the network, the assets are already here".
// bootstrap.mk honors it while the helper, not seeing it, fetches anyway, so
// an air-gapped or pre-vendored build fails at parse time, which is the exact
// case the flag exists to serve.
func TestSkipFetchHonoredWhenSetOnTheMakeCommandLine(t *testing.T) {
	dir := newConsumer(t)
	for name, body := range helperFiles() {
		// Leave the helper alone. newConsumer seeded .make with this
		// checkout's real script, and helperFiles carries a stub that exits 0
		// without doing anything. Writing that stub here would make the parse
		// succeed no matter what the flag does, so the test would pass against
		// the very bug it exists to catch.
		if name == "scripts/go-mk-bootstrap.sh" {
			continue
		}
		if name == "go.mk" {
			body = "help:\n\t@printf 'vendored\\n'\n"
		}
		writeAsset(t, dir, name, body)
	}

	output, code := runMakeWithCommandLineVars(t, dir, map[string]string{
		"GO_MK_SKIP_FETCH":    "1",
		"GO_MK_CODELOAD_BASE": unreachableCodeloadBase(t),
	})
	if code != 0 {
		t.Fatalf("parse exit = %d with GO_MK_SKIP_FETCH=1 on the command line and a complete .make, "+
			"want 0 (the flag must prevent every network access): %s", code, output)
	}
}

// consumerFiles is the served tree for a parse test. go.mk is a stub with a
// help target, so the parse succeeds without pulling the real engine.
//
// scripts/go-mk-bootstrap.sh, by contrast, must be the real script rather than
// a stub. The helper installs its own successor, so whatever the server serves
// under that name becomes the helper bootstrap.mk executes on the NEXT parse.
// Serving a stub therefore makes a consumer downgrade itself: the cold parse
// overwrites the seeded real helper with the stub, and the warm parse then runs
// a script that exits 0 without contacting anything, which reads as a passing
// parse that issued no requests.
func consumerFiles(t *testing.T) map[string]string {
	t.Helper()
	files := helperFiles()
	files["go.mk"] = "help:\n\t@printf 'stub help\\n'\n"
	helperBody, err := os.ReadFile(
		filepath.Join(repoRootForTest(t), "scripts", "go-mk-bootstrap.sh"))
	if err != nil {
		t.Fatalf("read scripts/go-mk-bootstrap.sh: %v", err)
	}
	files["scripts/go-mk-bootstrap.sh"] = string(helperBody)
	return files
}

func TestOfflineParseDoesNotDestroyCachedAssets(t *testing.T) {
	server := newFetchServer(t, consumerFiles(t))
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
	server := newFetchServer(t, consumerFiles(t))
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

// oldBootstrapMkSnapshot is bootstrap.mk exactly as committed before this
// task, kept as a fixed literal (not read from disk) so the mixed-version
// test below still exercises the pre-delegation shim after this task
// replaces the real file. It sets GO_MK_BOOTSTRAP_FETCHED but never
// GO_MK_PROVISION, since that variable does not exist until this task.
const oldBootstrapMkSnapshot = `# bootstrap.mk: tiny shim that fetches go-makefile assets and includes them.
# Consumer Makefiles set their identity vars (BINARY, CMD, VPKG, MODULES, etc.)
# then ` + "`include bootstrap.mk`" + `. Everything else (go.mk, golangci.yml, modules)
# is fetched at parse time and -included transitively.
#
# This file is canonical in agoodkind/go-makefile. Consumers commit a copy.
# Update path: edit go-makefile/bootstrap.mk, then refresh all consumer copies
# (one-off sync; not a long-term mechanism).

GO_MK_DEV_DIR  ?=
GO_MK_MODULES  ?=
GO_MK          := .make/go.mk
GO_MK_BASE_URL ?= https://raw.githubusercontent.com/agoodkind/go-makefile/main
GO_MK_API_REPO ?= agoodkind/go-makefile
GO_MK_API_REF  ?= main

define _go_mk_fetch
	if [ -n "$(GO_MK_DEV_DIR)" ] && [ -f "$(GO_MK_DEV_DIR)/$(1)" ]; then \
		cp "$(GO_MK_DEV_DIR)/$(1)" "$(2)"; \
	elif [ -s "$(2)" ]; then \
		: ; \
	elif curl -fsSL --connect-timeout 5 --max-time 10 --retry 3 --retry-delay 2 "$(GO_MK_BASE_URL)/$(1)" -o "$(2)" 2>/dev/null && [ -s "$(2)" ]; then \
		: ; \
	else \
		printf '%s\n' "error: $(1) fetch failed; no cache fallback (moratorium). Set GO_MK_DEV_DIR, or check network access to codeload.github.com and $(GO_MK_BASE_URL)" >&2; \
		exit 1; \
	fi
endef

define _go_mk_prime
	if [ -n "$(GO_MK_DEV_DIR)" ]; then \
		: ; \
	else \
		for asset in go.mk golangci.yml $(GO_MK_MODULES); do rm -f ".make/$$asset"; done; \
		tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/go-mk.XXXXXXXX") || exit 0; \
		if curl -fsSL --connect-timeout 5 --max-time 30 --retry 3 --retry-delay 2 "https://codeload.github.com/$(GO_MK_API_REPO)/tar.gz/$(GO_MK_API_REF)" 2>/dev/null | tar -xzf - -C "$$tmp" --strip-components 1 2>/dev/null; then \
			for asset in go.mk golangci.yml $(GO_MK_MODULES); do \
				if [ -f "$$tmp/$$asset" ]; then \
					mkdir -p "$$(dirname ".make/$$asset")"; \
					cp "$$tmp/$$asset" ".make/$$asset"; \
				fi; \
			done; \
		fi; \
		rm -rf "$$tmp"; \
	fi
endef

GO_MK_BOOTSTRAP_FETCHED := 1

define _go_mk_require_fetched
$(if $(wildcard $(1)),,$(error go-makefile expected $(1); rerun without GO_MK_SKIP_FETCH))
endef

ifeq ($(strip $(GO_MK_SKIP_FETCH)),1)
GO_MK_FETCH_CHECK := $(call _go_mk_require_fetched,$(GO_MK))
GO_MK_FETCH_CHECK += $(call _go_mk_require_fetched,.make/golangci.yml)
GO_MK_FETCH_CHECK += $(foreach m,$(GO_MK_MODULES),$(call _go_mk_require_fetched,.make/$(m)))
else

$(shell mkdir -p .make && { $(call _go_mk_prime); } 1>&2)
$(shell mkdir -p .make && { $(call _go_mk_fetch,go.mk,$(GO_MK)); } 1>&2)
$(shell { $(call _go_mk_fetch,golangci.yml,.make/golangci.yml); } 1>&2)
$(foreach m,$(GO_MK_MODULES),$(shell { $(call _go_mk_fetch,$(m),.make/$(m)); } 1>&2))

endif

# go.mk handles -including the modules at its tail (after all its variables
# are defined), so the modules see build-check etc. Don't duplicate
# the include here or every module target gets overriding-commands warnings.
-include $(GO_MK)
`

// TestOldBootstrapMkParsesWithCurrentGoMkAndNoGoMkProvision covers the
// rollout window this task's own delegation creates: until a consumer
// merges the PR this task requires, they keep running the old,
// pre-delegation bootstrap.mk snapshotted above, which never sets
// GO_MK_PROVISION (that variable does not exist yet in their committed
// copy). But bootstrap.mk always fetches or reuses go.mk fresh at parse
// time, so that old shim still lands the *current* go.mk (this repo's, as
// committed right now) in the same parse. go.mk must not depend on
// GO_MK_PROVISION for that combination to keep working, since it is what
// every consumer runs until they merge.
//
// This is fully hermetic (no network, no fetch server): GO_MK_DEV_DIR
// points at a directory seeded with the current go.mk, golangci.yml, and a
// scripts/go-mk-bin.sh sentinel. That sentinel is what makes go.mk resolve
// GO_MK_HELPER_DIR to the dev dir instead of .make/scripts, so it never
// requires scripts/go-mk-fetch-one.sh, scripts/go-mk-sync.sh, or
// notices.txt, none of which the old bootstrap.mk ever fetched.
func TestOldBootstrapMkParsesWithCurrentGoMkAndNoGoMkProvision(t *testing.T) {
	repoRoot := repoRootForTest(t)
	devDir := t.TempDir()

	goMkBody, err := os.ReadFile(filepath.Join(repoRoot, "go.mk"))
	if err != nil {
		t.Fatalf("read current go.mk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "go.mk"), goMkBody, 0o644); err != nil {
		t.Fatalf("write go.mk into dev dir: %v", err)
	}

	golangciBody, err := os.ReadFile(filepath.Join(repoRoot, "golangci.yml"))
	if err != nil {
		t.Fatalf("read current golangci.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "golangci.yml"), golangciBody, 0o644); err != nil {
		t.Fatalf("write golangci.yml into dev dir: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(devDir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir dev scripts dir: %v", err)
	}
	sentinel := filepath.Join(devDir, "scripts", "go-mk-bin.sh")
	if err := os.WriteFile(sentinel, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write go-mk-bin.sh sentinel: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bootstrap.mk"), []byte(oldBootstrapMkSnapshot), 0o644); err != nil {
		t.Fatalf("write old bootstrap.mk: %v", err)
	}
	makefile := "BINARY := probe\nCMD := ./cmd/probe\ninclude bootstrap.mk\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	output, code := runMake(t, dir, map[string]string{"GO_MK_DEV_DIR": devDir})
	if code != 0 {
		t.Fatalf("parse exit = %d, want 0 (old bootstrap.mk + current go.mk, GO_MK_PROVISION never set): %s", code, output)
	}
}

// TestHelperAcquisitionBoundedWhenBootstrapURLStalls covers
// _go_mk_get_bootstrap's own curl call, the one fetch that is
// consumer-committed rather than fetched, and therefore the one place a
// later hardening change cannot reach every consumer without another PR.
// It stalls the server GO_MK_BOOTSTRAP_BASE_URL points at (mirroring
// GO_MK_CODELOAD_BASE's role for the helper's own fetches) and asserts the
// acquisition gives up within a bounded wall-clock time, not merely that
// it eventually fails: an exit-code-only assertion would have passed at
// 134 seconds, which is how this exact class of bug (curl retrying a
// speed-limit or max-time abort as a transient error, uncapped) hid the
// first time in provision(). Unlike newConsumer, this deliberately does
// not seed .make/scripts/go-mk-bootstrap.sh, since the acquisition path
// under test only runs when no cached helper is already on disk.
func TestHelperAcquisitionBoundedWhenBootstrapURLStalls(t *testing.T) {
	server := newFetchServer(t, helperFiles())
	server.Stall(10 * time.Second)
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

	start := time.Now()
	output, code := runMake(t, dir, map[string]string{
		"GO_MK_BOOTSTRAP_BASE_URL": server.CodeloadBase(),
	})
	elapsed := time.Since(start)
	t.Logf("helper acquisition took %s against a stalled bootstrap URL", elapsed)

	if code == 0 {
		t.Fatalf("parse exit = 0, want non-zero against a stalled bootstrap URL: %s", output)
	}
	// Measured worst case is ~8s (2 attempts x ~3s speed-limit abort + 1
	// retry delay x 2s), the same shape as provision()'s own bound. 15s
	// gives margin for CI/host jitter while staying well clear of the
	// ~30s+ an uncapped retry cascade against this fetch would cost.
	const wallClockCeiling = 15 * time.Second
	if elapsed > wallClockCeiling {
		t.Fatalf("helper acquisition took %s against a stalled bootstrap URL, want under %s (a retried stall is not bounded)", elapsed, wallClockCeiling)
	}
}

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
	files := consumerFiles(t)
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
	// Two requests must reach THIS server: the old bootstrap.mk fetching the
	// archive itself, and go.mk's own _go_mk_prime running because
	// GO_MK_PROVISION is unset. The second one is the point. While the prime
	// held codeload.github.com as a literal it ignored the redirect and hit
	// production on every run of this test, so the server saw only one request
	// and the test silently depended on the network and on whatever was on
	// main rather than on the fixture above.
	requests := server.Requests()
	if len(requests) != 2 {
		t.Fatalf("server saw %d requests, want 2 (the old bootstrap fetch plus go.mk's own prime); "+
			"a missing one escaped to production instead of the test server", len(requests))
	}
	for index, request := range requests {
		if want := "/agoodkind/go-makefile/tar.gz/main"; request.Path != want {
			t.Fatalf("request %d path = %q, want %q", index, request.Path, want)
		}
	}
}

// TestGoMkSkipsItsOwnPrimeWhenHelperProvisioned covers the actual behavior
// this task's interface promises: go.mk skips its own prime when
// GO_MK_PROVISION shows the helper already provisioned this run. This is
// the one property TestOldBootstrapStillParsesWithNewGoMk does not
// actually exercise, since that test's own inline old-bootstrap snippet
// extracts the whole served tarball into .make/ itself, so every asset
// go.mk might need is already present regardless of whether go.mk's own
// prime also runs; it cannot tell "prime ran redundantly" apart from
// "prime correctly stayed out of the way."
//
// This test uses the real (new) bootstrap.mk via newConsumer, which sets
// GO_MK_PROVISION after the helper succeeds, paired with the real go.mk
// (not consumerFiles' stub) so go.mk's own prime-guard logic actually
// runs. go.mk's _go_mk_prime fetches straight from the real
// codeload.github.com, not the local GO_MK_CODELOAD_BASE test server, so
// if it ran a second time it would overwrite the served stub
// scripts/go-mk-fetch-one.sh (a few bytes, served only by the local
// fetchServer) with the real script's actual content pulled from the real
// internet, which this asserts against.
func TestGoMkSkipsItsOwnPrimeWhenHelperProvisioned(t *testing.T) {
	files := consumerFiles(t)
	realGoMk, err := os.ReadFile(filepath.Join(repoRootForTest(t), "go.mk"))
	if err != nil {
		t.Fatalf("read go.mk: %v", err)
	}
	files["go.mk"] = string(realGoMk)
	server := newFetchServer(t, files)
	dir := newConsumer(t)

	output, code := runMake(t, dir, map[string]string{"GO_MK_CODELOAD_BASE": server.CodeloadBase()})
	if code != 0 {
		t.Fatalf("parse exit = %d, want 0: %s", code, output)
	}

	const stub = "#!/usr/bin/env bash\nexit 0\n"
	if got := readAsset(t, dir, "scripts/go-mk-fetch-one.sh"); got != stub {
		t.Fatalf("scripts/go-mk-fetch-one.sh = %q, want the helper-provisioned stub %q preserved (go.mk's own prime must not run a second time)", got, stub)
	}
}
