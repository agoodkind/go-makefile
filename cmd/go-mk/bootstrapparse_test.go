package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
