.PHONY: build deploy clean help \
	lint lint-tools lint-golangci lint-golangci-baseline lint-files lint-diff lint-format lint-gocyclo fmt vet test govulncheck build-check check \
	staticcheck-extra staticcheck-extra-baseline staticcheck-extra-bin \
	baseline \
	go-mk-sync update-go-mk smoke-fetch

GO_MK_URL       := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK_CACHE     := $(HOME)/.cache/go-makefile/go.mk
GO_MK_BASE_URL  ?= https://raw.githubusercontent.com/agoodkind/go-makefile/main
GO_MK_API_BASE  ?= https://api.github.com/repos/agoodkind/go-makefile/contents
GO_MK_API_REF   ?= main
GO_MK_CACHE_DIR ?= $(or $(XDG_CACHE_HOME),$(HOME)/.cache)/go-makefile

# go-mk-fetch-one: fetch one asset from go-makefile (relative path, e.g.
# go-build.mk or golangci.yml) into .make/<path>. Fetch order:
#   1. GO_MK_DEV_DIR override (local-checkout copy, for iteration)
#   2. gh api (authenticated, no rate limit; requires `gh auth login`)
#   3. raw URL with cache-bust (anonymous CDN, may serve stale bytes for
#      several minutes after a push)
#   4. raw URL plain
#
# TODO(moratorium): the legacy ~/.cache/go-makefile fallback was removed
# because a stale cache silently masked an upstream breakage and froze every
# consumer on a broken go.mk for a full session. Restore the cache only after
# the primary fetch path has been demonstrably reliable for a sustained
# period (e.g., gh-api-first hits succeed across all dev machines without
# falling through to anonymous raw). Until then, fail loud rather than
# serve stale.
#
# All output goes to stderr; $(call ...) evaluates to the empty string so
# it's safe to use at the top level.
go-mk-fetch-one = $(shell { \
	mkdir -p .make; \
	target=".make/$(1)"; \
	if [ -n "$(GO_MK_DEV_DIR)" ] && [ -f "$(GO_MK_DEV_DIR)/$(1)" ]; then \
		cp "$(GO_MK_DEV_DIR)/$(1)" "$$target"; \
		printf '%s\n' "$(1): using dev override $(GO_MK_DEV_DIR)/$(1)"; \
	else \
		tmp="$$target.tmp"; \
		if command -v gh >/dev/null 2>&1 && gh api "repos/agoodkind/go-makefile/contents/$(1)?ref=$(GO_MK_API_REF)" -H "Accept: application/vnd.github.raw" > "$$tmp" 2>/dev/null \
			|| curl -fsSL --connect-timeout 5 --max-time 10 "$(GO_MK_BASE_URL)/$(1)?v=$$(date +%s)" -o "$$tmp" 2>/dev/null \
			|| curl -fsSL --connect-timeout 5 --max-time 10 "$(GO_MK_BASE_URL)/$(1)" -o "$$tmp" 2>/dev/null; then \
			[ -s "$$tmp" ] && mv "$$tmp" "$$target" || { rm -f "$$tmp"; printf '%s\n' "error: $(1) fetched empty body"; exit 1; }; \
		else \
			rm -f "$$tmp"; \
			printf '%s\n' "error: $(1) fetch failed; no cache fallback (moratorium). Run: gh auth login"; \
			exit 1; \
		fi; \
	fi; \
} 1>&2)

# GO_MK_MODULES: project sets a list of sibling .mk files to fetch and include.
# Example: GO_MK_MODULES := go-build.mk go-release.mk go-service.mk
# Set BEFORE `-include $(GO_MK)` in the project Makefile.
# Modules are fetched here at parse time but `-include`d at the END of go.mk
# so they see all of go.mk's definitions (default-build-deps etc.).
GO_MK_MODULES ?=
$(foreach m,$(GO_MK_MODULES),$(call go-mk-fetch-one,$(m)))

# Centralized golangci-lint config. The canonical config lives at
# go-makefile/golangci.yml. Consumers do not maintain their own .golangci.yml.
# Projects override by setting GOLANGCI_LINT_FLAGS before -include $(GO_MK).
GO_MK_GOLANGCI_CONFIG ?= .make/golangci.yml
$(call go-mk-fetch-one,golangci.yml)

GOLANGCI_LINT          ?= golangci-lint
GOLANGCI_LINT_TARGETS  ?= ./...
# LINT_CONCURRENCY caps the number of CPU workers golangci-lint spawns.
# Default is half the available cores so a `make build` does not pin the
# machine. Override with `make build LINT_CONCURRENCY=8` (or 0 for no cap).
LINT_CONCURRENCY       ?= $(shell n=$$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4); echo $$((n/2 > 0 ? n/2 : 1)))
GOLANGCI_LINT_FLAGS    ?= -c $(GO_MK_GOLANGCI_CONFIG) $(if $(filter-out 0,$(LINT_CONCURRENCY)),--concurrency=$(LINT_CONCURRENCY))
GOLANGCI_LINT_BASELINE ?= .golangci-lint-baseline.txt
GOLANGCI_LINT_BASELINE_RUNS ?= 3
GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS ?= _test\.go:
GOLANGCI_LINT_EXCLUDE_PATHS ?=
GOFUMPT                ?= gofumpt
GOIMPORTS              ?= goimports
GOCYCLO_OVER           ?= 30
GOCYCLO_TARGETS        ?= $$(find . -name '*.go' -not -name '*_test.go' -not -path './vendor/*' -not -path './gen/*' -not -path './third_party/*')
GOCYCLO_INSTALL        ?= github.com/fzipp/gocyclo/cmd/gocyclo@latest
GOLANGCI_LINT_INSTALL  ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
GOFUMPT_INSTALL        ?= mvdan.cc/gofumpt@v0.9.2
GOIMPORTS_INSTALL      ?= golang.org/x/tools/cmd/goimports@v0.44.0
GO_BUILD_OUTPUT        ?= $(if $(strip $(CMD)),$(BINARY),)
GO_BUILD_FLAGS         ?=
GO_BUILD_OUTPUT_FLAGS  ?= $(if $(strip $(GO_BUILD_OUTPUT)),-o $(GO_BUILD_OUTPUT),)
GO_BUILD_TARGETS       ?= $(if $(strip $(CMD)),$(CMD),./...)
GO_TEST_TARGETS        ?= ./...
GO_VET_TARGETS         ?= ./...
GOVULNCHECK_TARGETS    ?= ./...
GO_INSTALL_FLAGS       ?= $(filter-out -o %,$(GO_BUILD_FLAGS))
GO_INSTALL_TARGET      ?= $(CMD)
BUILD_CHECKS           ?= true

ifeq ($(BUILD_CHECKS),true)
default-build-deps := build-check
else
default-build-deps :=
endif

# Legacy build/deploy/clean targets. When go-build.mk is opted in via
# GO_MK_MODULES, it owns these and we skip the legacy definitions to avoid
# Make's "overriding commands" warning.
ifeq ($(filter go-build.mk,$(GO_MK_MODULES)),)
build: $(default-build-deps)
	go build $(GO_BUILD_OUTPUT_FLAGS) $(GO_BUILD_FLAGS) $(GO_BUILD_TARGETS)

deploy:
	@if [ -z "$(strip $(GO_INSTALL_TARGET))" ]; then echo "deploy: GO_INSTALL_TARGET is not set" >&2; exit 1; fi
	go install $(GO_INSTALL_FLAGS) $(GO_INSTALL_TARGET)

clean:
	@if [ -z "$(strip $(BINARY))" ]; then echo "clean: BINARY is not set (skipped)"; exit 0; fi
	rm -f $(BINARY)
endif

help:
	@printf '%s\n' 'Canonical entry points (run these):'
	@printf '  %-32s %s\n' 'build' 'vet + full lint + govulncheck, then go build'
	@printf '  %-32s %s\n' 'check' 'alias for lint (run every gate)'
	@printf '  %-32s %s\n' 'lint' 'just the full lint chain (no build, no test)'
	@printf '  %-32s %s\n' 'build-check' 'vet + lint + govulncheck (no build)'
	@printf '  %-32s %s\n' 'fmt' 'apply gofumpt + goimports'
	@printf '  %-32s %s\n' 'test' 'go test ./...'
	@printf '  %-32s %s\n' 'install / uninstall' 'atomic copy of dist/$$(BINARY) to $$(INSTALL_BIN)'
	@printf '\n%s\n' 'Scoped iteration (run all lint gates against just the files an agent has touched):'
	@printf '  %-32s %s\n' 'lint-diff' 'all gates against staged .go files (git diff --cached). Same as pre-commit.'
	@printf '  %-32s %s\n' 'lint-files LINT_FILES=...' 'all gates against a specific list of files. Baseline-gated.'
	@printf '  %-32s %s\n' '  BASELINE=""' 'disable baseline gates; show all findings on listed files'
	@printf '\n%s\n' 'Lint sub-targets (run individually only when iterating; usually run via build/check):'
	@printf '  %-32s %s\n' 'lint-tools' 'install golangci-lint, gofumpt, goimports'
	@printf '  %-32s %s\n' 'lint-golangci' 'golangci-lint with central golangci.yml + .golangci-lint-baseline.txt'
	@printf '  %-32s %s\n' 'lint-format' 'gofumpt + goimports diff (no edits, gate only)'
	@printf '  %-32s %s\n' 'lint-gocyclo' 'cyclomatic complexity gate'
	@printf '  %-32s %s\n' 'lint-deadcode' 'unreachable functions + .deadcode-baseline.txt + DEADCODE_EXCLUDE_PATHS'
	@printf '  %-32s %s\n' 'staticcheck-extra' 'strict custom analyzers + .staticcheck-extra-baseline.txt'
	@printf '\n%s\n' 'Refresh baselines after fixing in code first (do NOT use to silence findings):'
	@printf '  %-32s %s\n' 'baseline BASELINE_CONFIRM=1' 'rebuild all three baseline files (gated; ask user)'
	@printf '  %-32s %s\n' 'lint-golangci-baseline' 'rebuild .golangci-lint-baseline.txt'
	@printf '  %-32s %s\n' 'lint-deadcode-baseline' 'rebuild .deadcode-baseline.txt'
	@printf '  %-32s %s\n' 'staticcheck-extra-baseline' 'rebuild .staticcheck-extra-baseline.txt'
	@printf '\n%s\n' 'Pipeline maintenance:'
	@printf '  %-32s %s\n' 'go-mk-sync / update-go-mk' 'refresh go.mk + sibling modules + golangci.yml'
	@printf '  %-32s %s\n' 'smoke-fetch' 'force a network fetch (bypassing GO_MK_DEV_DIR) to verify the curl chain'
	@printf '  %-32s %s\n' 'deploy' 'go install $$(GO_INSTALL_TARGET) (legacy; prefer install)'

# Bypass: when BYPASS_LINT matches today's slugified BYPASS_TOKEN_CMD output,
# the lint chain reports findings but exits 0 (non-blocking). Composable:
# BYPASS_TOKEN_CMD defaults to today's Wikipedia featured article slug, but
# can be swapped to any rotating public-or-private endpoint that emits a
# string per day. This is a trapdoor for unblocking builds when lint itself
# is broken; it is not a routine path. Mismatched/stale tokens fall through
# silently, exposing no signal that the mechanism exists.
#
# Both sides (BYPASS_TOKEN_CMD output and user's BYPASS_LINT) are normalized
# through `_bypass_slugify` so unicode in article titles (e.g. Katipō,
# São_Paulo, Édith_Piaf) doesn't force the user to type non-ASCII characters.
BYPASS_LINT      ?=
BYPASS_TOKEN_CMD ?= curl -fsSL "https://en.wikipedia.org/api/rest_v1/feed/featured/$$(date -u +%Y/%m/%d)" | jq -r '.tfa.titles.canonical'

# _bypass_slugify reads stdin and writes a normalized lowercase ASCII slug.
# iconv //TRANSLIT folds accents (ō -> o); the tr step drops any artifact
# characters iconv leaves behind so the result is purely [a-z0-9_-].
# Wrapped in `{ ... ; true; }` so that macOS iconv's exit code 1 on
# transliteration warnings does not trip the recipe's pipefail.
define _bypass_slugify
	{ iconv -f UTF-8 -t ASCII//TRANSLIT 2>/dev/null || cat; } | LC_ALL=C tr -cd 'A-Za-z0-9_-' | LC_ALL=C tr 'A-Z' 'a-z'
endef

LINT_GATES := lint-tools lint-golangci lint-format lint-gocyclo lint-deadcode staticcheck-extra

lint:
	@bash -eu -o pipefail -c '\
		status=0; \
		$(MAKE) --no-print-directory $(LINT_GATES) || status=$$?; \
		[ "$$status" -eq 0 ] && exit 0; \
		bypass=$$(printf "%s" "$(BYPASS_LINT)" | $(_bypass_slugify)); \
		if [ -n "$$bypass" ]; then \
			expected=$$($(BYPASS_TOKEN_CMD) 2>/dev/null | $(_bypass_slugify) || true); \
			if [ -n "$$expected" ] && [ "$$bypass" = "$$expected" ]; then \
				if [ "$(BYPASS_CONFIRM)" = "1" ]; then \
					printf "\n***********************************************************************\n" >&2; \
					printf "*** LINT FINDINGS NON-BLOCKING via BYPASS_LINT=%s\n" "$$expected" >&2; \
					printf "*** Findings reported above but build proceeds. Do not merge without fixing.\n" >&2; \
					printf "***********************************************************************\n\n" >&2; \
					exit 0; \
				fi; \
				printf "\nIf you are sure, please re-run with BYPASS_CONFIRM=1\n\n" >&2; \
			fi; \
		fi; \
		exit "$$status"'

# go install at @latest issues HEAD requests to the GitHub Contents API to
# resolve module versions. When unauthenticated, GitHub rate-limits and the
# 403 stderr leaks even on retry-and-succeed. We capture stderr to a temp
# file and replay it only on failure so transient noise stays quiet while
# real install errors still surface.
lint-tools:
	@err=$$(mktemp -t go-mk-lint-tools.XXXXXX); \
	if ! { go install $(GOLANGCI_LINT_INSTALL) && \
	       go install $(GOFUMPT_INSTALL) && \
	       go install $(GOIMPORTS_INSTALL); } 2>"$$err"; then \
		cat "$$err" >&2; rm -f "$$err"; exit 1; \
	fi; \
	rm -f "$$err"

lint-golangci: lint-tools
	@bash -eu -o pipefail -c '\
		mkdir -p .make; \
		raw_output=".make/golangci-lint.raw.out"; \
		findings_output=".make/golangci-lint.out"; \
		baseline_output=".make/golangci-lint.baseline.out"; \
		excludes="$$(printf "%s,%s" "$(GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS)" "$(GOLANGCI_LINT_EXCLUDE_PATHS)")"; \
		status=0; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat" || true; fi; \
		}; \
		$(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) $(GOLANGCI_LINT_TARGETS) > "$$raw_output" 2>&1 || status=$$?; \
		grep -E "^[^[:space:]][^:]+:[0-9]+:[0-9]+: |^[^[:space:]].*\\([[:alnum:]_-]+\\)$$" "$$raw_output" \
			| awk -v pwd="$$PWD/" -v cwd="$(CURDIR)/" '"'"'{ if (index($$0, pwd)==1) $$0=substr($$0, length(pwd)+1); if (index($$0, cwd)==1) $$0=substr($$0, length(cwd)+1); while (index($$0, "../")==1) $$0=substr($$0, 4); print }'"'"' \
			| filter \
			| sort > "$$findings_output" || true; \
		if [ ! -f "$(GOLANGCI_LINT_BASELINE)" ]; then touch "$(GOLANGCI_LINT_BASELINE)"; fi; \
		tab=$$(printf "\t"); \
		metadata_prefix="$${tab}# golangci-lint:"; \
		while IFS= read -r baseline_line || [ -n "$$baseline_line" ]; do \
			case "$$baseline_line" in ""|\#*) continue ;; esac; \
			finding="$${baseline_line%%$${metadata_prefix}*}"; \
			[ -n "$$finding" ] && printf "%s\n" "$$finding"; \
		done < "$(GOLANGCI_LINT_BASELINE)" | filter | sort > "$$baseline_output"; \
		keyize() { \
			awk '"'"'{ if (match($$0, /:[0-9]+:[0-9]+:/)) out=substr($$0, 1, RSTART-1) ":::" substr($$0, RSTART+RLENGTH); else out=$$0; while (index(out, "../")==1) out=substr(out, 4); print out }'"'"' "$$1"; \
		}; \
		findings_keys=".make/golangci-lint.keys.out"; \
		baseline_keys=".make/golangci-lint.keys.baseline.out"; \
		keyize "$$findings_output" | sort -u > "$$findings_keys"; \
		keyize "$$baseline_output" | sort -u > "$$baseline_keys"; \
		new_keys_file=".make/golangci-lint.keys.new"; \
		gone_keys_file=".make/golangci-lint.keys.gone"; \
		comm -23 "$$findings_keys" "$$baseline_keys" > "$$new_keys_file" || true; \
		comm -13 "$$findings_keys" "$$baseline_keys" > "$$gone_keys_file" || true; \
		map_keys_to_originals() { \
			awk '"'"'NR==FNR{keyset[$$0]=1; next} { if (match($$0, /:[0-9]+:[0-9]+:/)) k = substr($$0, 1, RSTART-1) ":::" substr($$0, RSTART+RLENGTH); else k = $$0; while (index(k, "../")==1) k = substr(k, 4); if (k in keyset) print }'"'"' "$$1" "$$2"; \
		}; \
		new=$$(map_keys_to_originals "$$new_keys_file" "$$findings_output"); \
		if [ -n "$$new" ]; then \
			echo "NEW golangci-lint findings:"; \
			echo "$$new"; \
			echo ""; \
			echo "Fix these findings in code. Do not disable, silence, weaken, or otherwise circumvent the checks."; \
			exit 1; \
		fi; \
		gone=$$(map_keys_to_originals "$$gone_keys_file" "$$baseline_output"); \
		if [ -n "$$gone" ]; then \
			echo "RESOLVED golangci-lint findings:"; \
			echo "$$gone"; \
		fi; \
		n=$$(wc -l < "$$findings_output"); \
		echo "golangci-lint: OK ($$n findings)"; \
		if [ "$$status" -ne 0 ] && [ ! -s "$$findings_output" ]; then cat "$$raw_output"; exit "$$status"; fi'

# lint-files runs golangci-lint scoped to LINT_FILES with the central config
# but WITHOUT the baseline gate. Use it while iterating on a change set,
# especially when parallel agents touch different parts of the tree and want
# to lint only their files. Switch to `make lint` (or check) for the full
# baseline-gated pipeline before commit.
#
# Examples:
#   make lint-files LINT_FILES="cmd/foo/main.go cmd/foo/router.go"
#   make lint-files LINT_FILES=./internal/auth/...
#
# Other linters accept the same scope via their own *_TARGETS env vars,
# e.g. make staticcheck-extra STATICCHECK_EXTRA_TARGETS=./internal/auth/...
LINT_FILES ?= ./...

.PHONY: lint-files
# BASELINE: which file to gate findings against. Default = the canonical
# golangci baseline file. Set BASELINE="" to disable the gate (all
# findings on listed files surface). Set BASELINE=other.txt to use any
# alternate baseline file.
BASELINE ?= $(GOLANGCI_LINT_BASELINE)

# lint-diff: lint exactly the .go files in the current `git diff --cached`
# (staged changes). Same logic the pre-commit hook uses, exposed as a
# target so agents can run it directly. Pure alias around lint-files.
# TODO: smarter mode that filters findings to only changed lines (via
# golangci-lint --new-from-rev=HEAD); deferred until line-level filtering
# is needed widely.
lint-diff:
	@bash -eu -o pipefail -c '\
		files=$$(git diff --cached --name-only --relative --diff-filter=ACM 2>/dev/null | grep "\.go$$" | tr "\n" " " || true); \
		[ -z "$$files" ] && { echo "lint-diff: no staged .go files"; exit 0; }; \
		$(MAKE) --no-print-directory lint-files LINT_FILES="$$files" BASELINE="$(BASELINE)"'

lint-files: lint-tools staticcheck-extra-bin
	@bash -eu -o pipefail -c '\
		[ -z "$(LINT_FILES)" ] && { echo "lint-files: LINT_FILES is empty"; exit 0; }; \
		pkgs=$$(printf "%s\n" $(LINT_FILES) | xargs -n1 dirname | sort -u | awk "{print \"./\" \$$0}" | tr "\n" " "); \
		files="$(LINT_FILES)"; \
		gate_disabled="$$([ -z "$(BASELINE)" ] && echo 1 || echo 0)"; \
		run_gate() { \
			local name="$$1" cmd="$$2" baseline="$$3"; \
			local raw filtered findings new bkeys st; \
			raw=$$(mktemp); \
			eval "$$cmd" > "$$raw" 2>&1 || true; \
			findings=$$(awk -v pwd="$$PWD/" -v cwd="$(CURDIR)/" '"'"'{ if (index($$0, pwd)==1) $$0=substr($$0, length(pwd)+1); if (index($$0, cwd)==1) $$0=substr($$0, length(cwd)+1); while (index($$0, "../")==1) $$0=substr($$0, 4); print }'"'"' "$$raw" | grep -E "^[^:]+\.go:[0-9]+:[0-9]+: " || true); \
			rm -f "$$raw"; \
			filtered=$$(echo "$$findings" | awk -v files="$$files" '"'"'BEGIN { n=split(files, ff, /[ \t]+/); for (i=1; i<=n; i++) if (ff[i] != "") keep[ff[i]]=1 } { for (f in keep) if (index($$0, f ":") == 1) { print; next } }'"'"'); \
			[ -z "$$filtered" ] && { echo "$$name: OK (0 findings on listed files)"; return 0; }; \
			if [ "$$gate_disabled" = "1" ]; then echo "$$name findings on listed files:"; echo "$$filtered"; return 1; fi; \
			bkeys=$$(mktemp); \
			[ -f "$$baseline" ] && awk '"'"'function k(s,    o) { if (match(s, /:[0-9]+:[0-9]+:/)) o = substr(s, 1, RSTART-1) ":::" substr(s, RSTART+RLENGTH); else o = s; while (index(o, "../")==1) o = substr(o, 4); return o } /^[ \t]*$$/||/^#/{next} { i=index($$0, "\t"); f=(i>0)?substr($$0, 1, i-1):$$0; print k(f) }'"'"' "$$baseline" > "$$bkeys"; \
			new=$$(echo "$$filtered" | awk -v bkeys="$$bkeys" '"'"'function k(s,    o) { if (match(s, /:[0-9]+:[0-9]+:/)) o = substr(s, 1, RSTART-1) ":::" substr(s, RSTART+RLENGTH); else o = s; while (index(o, "../")==1) o = substr(o, 4); return o } BEGIN { while ((getline x < bkeys) > 0) bk[x]=1 } { if (!(k($$0) in bk)) print }'"'"'); \
			rm -f "$$bkeys"; \
			[ -z "$$new" ] && { echo "$$name: OK (0 new findings vs $$baseline)"; return 0; }; \
			echo "$$name NEW findings on listed files (vs $$baseline):"; \
			echo "$$new"; \
			return 1; \
		}; \
		status=0; \
		run_gate golangci-lint "$(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) $$pkgs" "$(GOLANGCI_LINT_BASELINE)" || status=1; \
		run_gate staticcheck-extra ".make/staticcheck-extra $(STATICCHECK_EXTRA_FLAGS) $$pkgs" "$(STATICCHECK_EXTRA_BASELINE)" || status=1; \
		if [ "$$status" -ne 0 ] && [ "$$gate_disabled" != "1" ]; then \
			echo ""; \
			echo "Run with BASELINE=\"\" to see all findings (skip baseline gate)."; \
		fi; \
		exit "$$status"'

lint-golangci-baseline: lint-tools
	@bash -eu -o pipefail -c '\
		mkdir -p .make "$$(dirname "$(GOLANGCI_LINT_BASELINE)")"; \
		raw_output=".make/golangci-lint-baseline.raw.out"; \
		findings_output=".make/golangci-lint-baseline.out"; \
		new_baseline=".make/golangci-lint-baseline.new"; \
		baseline_output=".make/golangci-lint-baseline.baseline.out"; \
		excludes="$$(printf "%s,%s" "$(GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS)" "$(GOLANGCI_LINT_EXCLUDE_PATHS)")"; \
		status=0; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat" || true; fi; \
		}; \
		: > "$$raw_output"; \
		: > "$$findings_output"; \
		for run_index in $$(seq 1 $(GOLANGCI_LINT_BASELINE_RUNS)); do \
			run_raw_output=".make/golangci-lint-baseline.$$run_index.raw.out"; \
			run_findings_output=".make/golangci-lint-baseline.$$run_index.out"; \
			run_status=0; \
			$(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) $(GOLANGCI_LINT_TARGETS) > "$$run_raw_output" 2>&1 || run_status=$$?; \
			cat "$$run_raw_output" >> "$$raw_output"; \
			grep -E "^[^[:space:]][^:]+:[0-9]+:[0-9]+: |^[^[:space:]].*\\([[:alnum:]_-]+\\)$$" "$$run_raw_output" \
				| awk -v pwd="$$PWD/" -v cwd="$(CURDIR)/" '"'"'{ if (index($$0, pwd)==1) $$0=substr($$0, length(pwd)+1); if (index($$0, cwd)==1) $$0=substr($$0, length(cwd)+1); while (index($$0, "../")==1) $$0=substr($$0, 4); print }'"'"' \
				| filter \
				| sort > "$$run_findings_output" || true; \
			cat "$$run_findings_output" >> "$$findings_output"; \
			if [ "$$run_status" -ne 0 ]; then status="$$run_status"; fi; \
		done; \
		sort -u "$$findings_output" > "$$findings_output.merged"; \
		mv "$$findings_output.merged" "$$findings_output"; \
		if [ ! -f "$(GOLANGCI_LINT_BASELINE)" ]; then touch "$(GOLANGCI_LINT_BASELINE)"; fi; \
		now=$$(date -u +"%Y-%m-%dT%H:%M:%SZ"); \
		tab=$$(printf "\t"); \
		metadata_prefix="$${tab}# golangci-lint:"; \
		while IFS= read -r baseline_line || [ -n "$$baseline_line" ]; do \
			case "$$baseline_line" in ""|\#*) continue ;; esac; \
			baseline_finding="$${baseline_line%%$${metadata_prefix}*}"; \
			[ -n "$$baseline_finding" ] && printf "%s\n" "$$baseline_finding"; \
		done < "$(GOLANGCI_LINT_BASELINE)" | filter | sort -u > "$$baseline_output"; \
		awk '"'"'function key(s,    out) { if (match(s, /:[0-9]+:[0-9]+:/)) out = substr(s, 1, RSTART-1) ":::" substr(s, RSTART+RLENGTH); else out = s; while (index(out, "../")==1) out = substr(out, 4); return out } { k = key($$0); if (!(k in pick) || (substr(pick[k], 1, 3) == "../" && substr($$0, 1, 3) != "../")) pick[k] = $$0 } END { for (k in pick) print pick[k] }'"'"' "$$findings_output" "$$baseline_output" | sort > "$$findings_output.merged"; \
		mv "$$findings_output.merged" "$$findings_output"; \
		printf "# golangci-lint: generated_at=%s\n" "$$now" > "$$new_baseline"; \
		awk -v now="$$now" -v mp="$${metadata_prefix}" -v lname=golangci-lint -v kmf="$(GOLANGCI_LINT_BASELINE)" '"'"'function key(s,    out) { if (match(s, /:[0-9]+:[0-9]+:/)) out = substr(s, 1, RSTART-1) ":::" substr(s, RSTART+RLENGTH); else out = s; while (index(out, "../")==1) out = substr(out, 4); return out } BEGIN { while ((getline line < kmf) > 0) { if (line ~ /^#/) continue; if (line ~ /^[ \t]*$$/) continue; idx = index(line, mp); if (idx > 0) { finding = substr(line, 1, idx-1); meta = substr(line, idx + length(mp)); fa = ""; n = split(meta, ff, " "); for (i = 1; i <= n; i++) if (ff[i] ~ /^first_added=/) fa = substr(ff[i], 13); km[key(finding)] = fa } else km[key(line)] = "" } close(kmf) } { k = key($$0); fa = (k in km) ? km[k] : ""; if (fa == "") fa = now; printf "%s\t# %s:first_added=%s last_seen=%s\n", $$0, lname, fa, now }'"'"' "$$findings_output" >> "$$new_baseline"; \
		mv "$$new_baseline" "$(GOLANGCI_LINT_BASELINE)"; \
		n=$$(wc -l < "$$findings_output"); \
		echo "golangci-lint: baseline $(GOLANGCI_LINT_BASELINE) refreshed ($$n findings)"; \
		if [ "$$status" -ne 0 ] && [ "$$n" -eq 0 ]; then cat "$$raw_output"; exit "$$status"; fi'

lint-format:
	@diff_output=$$($(GOLANGCI_LINT) fmt --diff $(GOLANGCI_LINT_FLAGS) $(GOLANGCI_LINT_TARGETS)); \
	if [ -n "$$diff_output" ]; then \
		echo "golangci-lint formatters need to update:"; \
		printf '%s\n' "$$diff_output"; \
		echo "run make fmt"; \
		exit 1; \
	fi

lint-gocyclo:
	@err=$$(mktemp -t go-mk-gocyclo.XXXXXX); \
	if ! go install $(GOCYCLO_INSTALL) 2>"$$err"; then \
		cat "$$err" >&2; rm -f "$$err"; exit 1; \
	fi; \
	rm -f "$$err"; \
	"$$(go env GOPATH)/bin/gocyclo" -over $(GOCYCLO_OVER) $(GOCYCLO_TARGETS)

fmt: lint-tools
	$(GOLANGCI_LINT) fmt $(GOLANGCI_LINT_FLAGS) $(GOLANGCI_LINT_TARGETS)

vet:
	go vet $(GO_VET_TARGETS)

test:
	go test $(GO_TEST_TARGETS)

govulncheck:
	@err=$$(mktemp -t go-mk-vuln.XXXXXX); \
	if ! go install golang.org/x/vuln/cmd/govulncheck@latest 2>"$$err"; then \
		cat "$$err" >&2; rm -f "$$err"; exit 1; \
	fi; \
	rm -f "$$err"; \
	"$$(go env GOPATH)/bin/govulncheck" $(GOVULNCHECK_TARGETS)

# lint-deadcode runs golang.org/x/tools/cmd/deadcode and gates new findings
# against a baseline file. Same pattern as staticcheck-extra: existing dead
# code is captured in .deadcode-baseline.txt, only new findings fail the
# build. Refresh with `make lint-deadcode-baseline` after intentionally
# removing dead code or when the analyzer's reachability rules change.
DEADCODE_INSTALL          ?= golang.org/x/tools/cmd/deadcode@latest
DEADCODE_TARGETS          ?= ./...
DEADCODE_BASELINE         ?= .deadcode-baseline.txt
DEADCODE_DEFAULT_EXCLUDE_PATHS ?= _test\.go:
DEADCODE_EXCLUDE_PATHS    ?=

.PHONY: lint-deadcode lint-deadcode-baseline

lint-deadcode:
	@bash -eu -o pipefail -c '\
		err=$$(mktemp -t go-mk-deadcode-install.XXXXXX); \
		if ! go install $(DEADCODE_INSTALL) 2>"$$err"; then \
			cat "$$err" >&2; rm -f "$$err"; exit 1; \
		fi; \
		rm -f "$$err"; \
		mkdir -p .make; \
		raw=".make/deadcode.raw.out"; \
		findings=".make/deadcode.out"; \
		baseline=".make/deadcode.baseline.out"; \
		excludes="$$(printf "%s,%s" "$(DEADCODE_DEFAULT_EXCLUDE_PATHS)" "$(DEADCODE_EXCLUDE_PATHS)")"; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat" || true; fi; \
		}; \
		"$$(go env GOPATH)/bin/deadcode" $(DEADCODE_TARGETS) > "$$raw" 2>&1 || true; \
		grep -E "^[^[:space:]][^:]+:[0-9]+:[0-9]+:" "$$raw" \
			| awk -v pwd="$$PWD/" -v cwd="$(CURDIR)/" '"'"'{ if (index($$0, pwd)==1) $$0=substr($$0, length(pwd)+1); if (index($$0, cwd)==1) $$0=substr($$0, length(cwd)+1); while (index($$0, "../")==1) $$0=substr($$0, 4); print }'"'"' \
			| filter \
			| sort > "$$findings" || true; \
		if [ ! -f "$(DEADCODE_BASELINE)" ]; then touch "$(DEADCODE_BASELINE)"; fi; \
		tab=$$(printf "\t"); \
		metadata_prefix="$${tab}# deadcode:"; \
		while IFS= read -r baseline_line || [ -n "$$baseline_line" ]; do \
			case "$$baseline_line" in ""|\#*) continue ;; esac; \
			finding="$${baseline_line%%$${metadata_prefix}*}"; \
			[ -n "$$finding" ] && printf "%s\n" "$$finding"; \
		done < "$(DEADCODE_BASELINE)" | filter | sort > "$$baseline" || true; \
		keyize() { awk '"'"'{ if (match($$0, /:[0-9]+:[0-9]+:/)) out=substr($$0, 1, RSTART-1) ":::" substr($$0, RSTART+RLENGTH); else out=$$0; while (index(out, "../")==1) out=substr(out, 4); print out }'"'"' "$$1"; }; \
		findings_keys=".make/deadcode.keys.out"; \
		baseline_keys=".make/deadcode.keys.baseline.out"; \
		new_keys=".make/deadcode.keys.new"; \
		gone_keys=".make/deadcode.keys.gone"; \
		keyize "$$findings" | sort -u > "$$findings_keys"; \
		keyize "$$baseline" | sort -u > "$$baseline_keys"; \
		comm -23 "$$findings_keys" "$$baseline_keys" > "$$new_keys" || true; \
		comm -13 "$$findings_keys" "$$baseline_keys" > "$$gone_keys" || true; \
		map_keys() { awk '"'"'NR==FNR{keyset[$$0]=1; next} { if (match($$0, /:[0-9]+:[0-9]+:/)) k = substr($$0, 1, RSTART-1) ":::" substr($$0, RSTART+RLENGTH); else k = $$0; while (index(k, "../")==1) k = substr(k, 4); if (k in keyset) print }'"'"' "$$1" "$$2"; }; \
		new=$$(map_keys "$$new_keys" "$$findings"); \
		if [ -n "$$new" ]; then \
			echo "NEW deadcode findings:"; \
			echo "$$new"; \
			echo ""; \
			echo "Remove the dead code or document why it stays. Do not silence the check."; \
			exit 1; \
		fi; \
		gone=$$(map_keys "$$gone_keys" "$$baseline"); \
		if [ -n "$$gone" ]; then \
			echo "RESOLVED deadcode findings:"; \
			echo "$$gone"; \
		fi; \
		n=$$(wc -l < "$$findings"); \
		echo "deadcode: OK ($$n findings)"'

lint-deadcode-baseline:
	@bash -eu -o pipefail -c '\
		err=$$(mktemp -t go-mk-deadcode-install.XXXXXX); \
		if ! go install $(DEADCODE_INSTALL) 2>"$$err"; then \
			cat "$$err" >&2; rm -f "$$err"; exit 1; \
		fi; \
		rm -f "$$err"; \
		mkdir -p .make "$$(dirname "$(DEADCODE_BASELINE)")"; \
		raw=".make/deadcode-baseline.raw.out"; \
		findings=".make/deadcode-baseline.out"; \
		excludes="$$(printf "%s,%s" "$(DEADCODE_DEFAULT_EXCLUDE_PATHS)" "$(DEADCODE_EXCLUDE_PATHS)")"; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat" || true; fi; \
		}; \
		"$$(go env GOPATH)/bin/deadcode" $(DEADCODE_TARGETS) > "$$raw" 2>&1 || true; \
		grep -E "^[^[:space:]][^:]+:[0-9]+:[0-9]+:" "$$raw" \
			| awk -v pwd="$$PWD/" -v cwd="$(CURDIR)/" '"'"'{ if (index($$0, pwd)==1) $$0=substr($$0, length(pwd)+1); if (index($$0, cwd)==1) $$0=substr($$0, length(cwd)+1); while (index($$0, "../")==1) $$0=substr($$0, 4); print }'"'"' \
			| filter \
			| sort -u > "$$findings" || true; \
		if [ ! -f "$(DEADCODE_BASELINE)" ]; then touch "$(DEADCODE_BASELINE)"; fi; \
		now=$$(date -u +"%Y-%m-%dT%H:%M:%SZ"); \
		tab=$$(printf "\t"); \
		metadata_prefix="$${tab}# deadcode:"; \
		new_baseline=".make/deadcode-baseline.new"; \
		printf "# deadcode: generated_at=%s\n" "$$now" > "$$new_baseline"; \
		awk -v now="$$now" -v mp="$${metadata_prefix}" -v lname=deadcode -v kmf="$(DEADCODE_BASELINE)" '"'"'function key(s,    out) { if (match(s, /:[0-9]+:[0-9]+:/)) out = substr(s, 1, RSTART-1) ":::" substr(s, RSTART+RLENGTH); else out = s; while (index(out, "../")==1) out = substr(out, 4); return out } BEGIN { while ((getline line < kmf) > 0) { if (line ~ /^#/) continue; if (line ~ /^[ \t]*$$/) continue; idx = index(line, mp); if (idx > 0) { finding = substr(line, 1, idx-1); meta = substr(line, idx + length(mp)); fa = ""; n = split(meta, ff, " "); for (i = 1; i <= n; i++) if (ff[i] ~ /^first_added=/) fa = substr(ff[i], 13); km[key(finding)] = fa } else km[key(line)] = "" } close(kmf) } { k = key($$0); fa = (k in km) ? km[k] : ""; if (fa == "") fa = now; printf "%s\t# %s:first_added=%s last_seen=%s\n", $$0, lname, fa, now }'"'"' "$$findings" >> "$$new_baseline"; \
		mv "$$new_baseline" "$(DEADCODE_BASELINE)"; \
		n=$$(wc -l < "$$findings"); \
		echo "deadcode: baseline $(DEADCODE_BASELINE) refreshed ($$n findings)"'

build-check: vet lint govulncheck

# `check` is an alias for `lint`. Both run every lint gate (golangci-lint,
# format, gocyclo, deadcode, staticcheck-extra). To build + test, use
# `make build` (which runs lint via build-check) and `make test`.
check: lint

# baseline refreshes every gate's baseline file in one shot. Gated behind
# BASELINE_CONFIRM because baselining blindly hides defects: an agent that
# treats baseline as "make the lint warnings go away" silently weakens the
# central pipeline. The expected workflow is read findings, attempt to fix
# them in code, and only then accept the residual into the baseline with
# the user's explicit consent.
#
# Run with:
#   make baseline BASELINE_CONFIRM=1
#
# Individual baseline targets (lint-golangci-baseline, lint-deadcode-baseline,
# staticcheck-extra-baseline) remain available when only one needs refreshing.
.PHONY: baseline
baseline:
	@case "$(BASELINE_CONFIRM)" in \
		1|y|yes|Y|YES) ;; \
		*) \
			printf '%s\n' \
				"baseline refresh refused without explicit confirmation." \
				"" \
				"This target rewrites .golangci-lint-baseline.txt, .deadcode-baseline.txt," \
				"and .staticcheck-extra-baseline.txt to match the current finding set." \
				"" \
				"Agents: do NOT run this to silence lint warnings. The expected workflow is:" \
				"  1. Read findings: make lint, make lint-deadcode, make staticcheck-extra." \
				"  2. Faithfully attempt to fix each finding in code." \
				"  3. After fixing what you can, ask the user to confirm before baselining the rest." \
				"  4. Only then run: make baseline BASELINE_CONFIRM=1" \
				"" \
				"Baselining without fixing first hides real defects and degrades the pipeline." >&2; \
			exit 1 ;; \
	esac
	@$(MAKE) --no-print-directory lint-golangci-baseline
	@$(MAKE) --no-print-directory lint-deadcode-baseline
	@$(MAKE) --no-print-directory staticcheck-extra-baseline

# ---------------------------------------------------------------------------
# staticcheck-extra: AST analyzer pass with a baseline-diff gate so only NEW
# findings fail the build. The default source is the analyzer set bundled
# with go-makefile itself (github.com/agoodkind/go-makefile/staticcheck).
#
# Resolution order for the analyzer binary:
#   1. STATICCHECK_EXTRA_BIN              (explicit path to a prebuilt binary)
#   2. STATICCHECK_EXTRA_BUILD_REPO + _PKG (build from a local checkout)
#   3. STATICCHECK_EXTRA_INSTALL          (go install <module>@<version>)
#
# The default is option 3 with a pinned go-makefile module path, so any
# project that pulls in go.mk gets the analyzer set with zero extra config:
#
#   STATICCHECK_EXTRA_INSTALL = github.com/agoodkind/go-makefile/staticcheck/cmd/staticcheck-extra@latest
#
# Optional knobs:
#   STATICCHECK_EXTRA_FLAGS         args passed to the analyzer (default
#                                   enables every bundled check)
#   STATICCHECK_EXTRA_TARGETS       packages to analyze         (default ./...)
#   STATICCHECK_EXTRA_BASELINE      baseline file               (default .staticcheck-extra-baseline.txt)
#   STATICCHECK_EXTRA_EXCLUDE_PATHS comma-separated grep -E patterns matched
#                                   against finding lines (file:line:col:msg).
#                                   Any line that matches is dropped before
#                                   the baseline diff. Use for generated code
#                                   (e.g. "\.pb\.go:") or vendored paths.
#
# Behaviour:
#   - If no source is configured (all three resolution paths empty), target
#     is a no-op (announces "skipped").
#   - The bundled analyzer set is the default, installed via `go install`
#     into $(go env GOPATH)/bin on first run, then cached. If the cached
#     binary does not support the requested analyzer flags, it is rebuilt or
#     reinstalled before analysis starts.
#   - Excluded lines are dropped before baseline comparison.
#   - Findings are diffed against the baseline. NEW findings exit non-zero.
#     RESOLVED findings print without failing.
#   - `make staticcheck-extra-baseline` re-captures the current findings and
#     records generated_at for the file plus first_added and last_seen UTC
#     timestamps for each entry.
# ---------------------------------------------------------------------------
STATICCHECK_EXTRA_BIN           ?=
# When GO_MK_DEV_DIR is set and the dev checkout contains the analyzer
# source, build the binary from that checkout rather than from
# `go install ...@latest`. Without this, dev `go.mk` could declare new
# analyzer flags (e.g. -thin_wrapper_to_launderable_call) that the
# remote-installed binary does not yet support, because the local
# checkout has unpushed commits. Building from the same source as the
# flags eliminates that drift class entirely.
STATICCHECK_EXTRA_BUILD_REPO    ?= $(if $(and $(GO_MK_DEV_DIR),$(wildcard $(GO_MK_DEV_DIR)/staticcheck/cmd/staticcheck-extra)),$(GO_MK_DEV_DIR)/staticcheck)
STATICCHECK_EXTRA_BUILD_PKG     ?= $(if $(STATICCHECK_EXTRA_BUILD_REPO),./cmd/staticcheck-extra)
STATICCHECK_EXTRA_INSTALL       ?= github.com/agoodkind/go-makefile/staticcheck/cmd/staticcheck-extra@latest
STATICCHECK_EXTRA_CORE_FLAGS    ?= \
	-slog_error_without_err \
	-banned_direct_output \
	-hot_loop_info_log \
	-missing_boundary_log \
	-no_any_or_empty_interface
STATICCHECK_EXTRA_STRICT_FLAGS  ?= \
	-wrapped_error_without_slog \
	-os_exit_outside_main \
	-context_todo_in_production \
	-time_sleep_in_production \
	-panic_in_production \
	-time_now_outside_clock \
	-goroutine_without_recover \
	-slog_missing_trace_id \
	-grpc_handler_missing_peer_enrichment \
	-nolint_ban \
	-string_switch_should_be_enum \
	-thin_wrapper_to_launderable_call
STATICCHECK_EXTRA_FLAGS         ?= $(STATICCHECK_EXTRA_CORE_FLAGS) $(STATICCHECK_EXTRA_STRICT_FLAGS)
STATICCHECK_EXTRA_TARGETS       ?= ./...
STATICCHECK_EXTRA_BASELINE      ?= .staticcheck-extra-baseline.txt
STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS ?= _test\.go:
STATICCHECK_EXTRA_EXCLUDE_PATHS ?=

# Variable resolution and build are deferred to recipe time so the project
# Makefile can set STATICCHECK_EXTRA_* either before or after `-include`.
# Use $(MAKE) recursion for the build path so $(shell ...) sees the final
# variable values.

staticcheck-extra-bin:
	@bash -eu -c '\
		bin="$(STATICCHECK_EXTRA_BIN)"; \
		repo="$(STATICCHECK_EXTRA_BUILD_REPO)"; \
		pkg="$(STATICCHECK_EXTRA_BUILD_PKG)"; \
		install="$(STATICCHECK_EXTRA_INSTALL)"; \
		flags="$(STATICCHECK_EXTRA_FLAGS)"; \
		missing_flags() { \
			candidate="$$1"; \
			available=$$("$$candidate" -flags 2>/dev/null || true); \
			for flag in $$flags; do \
				name="$${flag#-}"; \
				printf "%s\n" "$$available" | grep -q "\"Name\": \"$$name\"" || return 0; \
			done; \
			return 1; \
		}; \
		build_from_repo() { \
			mkdir -p .make; \
			out="$(CURDIR)/.make/staticcheck-extra"; \
			cd "$$repo" && go build -o "$$out" "$$pkg"; \
		}; \
		install_binary() { \
			base=$$(basename "$${install%%@*}"); \
			gobin=$$(go env GOPATH)/bin; \
			installed="$$gobin/$$base"; \
			err_log=$$(mktemp -t staticcheck-extra-install.XXXXXX); \
			if ! GOPROXY=direct GONOSUMDB=github.com/agoodkind/go-makefile,github.com/agoodkind/go-makefile/staticcheck GOBIN="$$gobin" go install "$$install" 2>"$$err_log"; then \
				cat "$$err_log" >&2; \
				rm -f "$$err_log"; \
				return 1; \
			fi; \
			rm -f "$$err_log"; \
			ln -sf "$$installed" "$(CURDIR)/.make/staticcheck-extra"; \
		}; \
		if [ -n "$$bin" ]; then \
			[ -x "$$bin" ] || { echo "staticcheck-extra: $$bin not executable"; exit 1; }; \
			missing_flags "$$bin" && { echo "staticcheck-extra: $$bin does not support requested flags"; exit 1; }; \
			exit 0; \
		fi; \
		if [ -n "$$repo" ]; then \
			if [ ! -d "$$repo" ]; then \
				echo "staticcheck-extra: build repo $$repo not present; skipping"; exit 0; \
			fi; \
			if [ -z "$$pkg" ]; then \
				echo "staticcheck-extra: STATICCHECK_EXTRA_BUILD_PKG not set"; exit 1; \
			fi; \
			mkdir -p .make; \
			out="$(CURDIR)/.make/staticcheck-extra"; \
			newest_src=$$(find "$$repo" -name "*.go" -newer "$$out" 2>/dev/null | head -1 || true); \
			if [ ! -x "$$out" ] || [ -n "$$newest_src" ] || missing_flags "$$out"; then \
				build_from_repo; \
			fi; \
			exit 0; \
		fi; \
		if [ -z "$$install" ]; then exit 0; fi; \
		mkdir -p .make; \
		out="$(CURDIR)/.make/staticcheck-extra"; \
		base=$$(basename "$${install%%@*}"); \
		gobin=$$(go env GOPATH)/bin; \
		installed="$$gobin/$$base"; \
		case "$$install" in *@latest) at_latest=1 ;; *) at_latest=0 ;; esac; \
		if [ ! -x "$$installed" ] || [ "$$at_latest" = "1" ] || missing_flags "$$installed"; then \
			install_binary; \
		else \
			ln -sf "$$installed" "$$out"; \
		fi'

staticcheck-extra: staticcheck-extra-bin
	@bash -eu -o pipefail -c '\
		bin="$(STATICCHECK_EXTRA_BIN)"; \
		[ -z "$$bin" ] && [ -x .make/staticcheck-extra ] && bin=".make/staticcheck-extra"; \
		if [ -z "$$bin" ]; then \
			echo "staticcheck-extra: not configured (skipped)"; exit 0; \
		fi; \
		if [ ! -x "$$bin" ]; then \
			echo "staticcheck-extra: binary $$bin not executable; skipping"; exit 0; \
		fi; \
		mkdir -p .make; \
		excludes="$$(printf "%s,%s" "$(STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS)" "$(STATICCHECK_EXTRA_EXCLUDE_PATHS)")"; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat" || true; fi; \
		}; \
		"$$bin" $(STATICCHECK_EXTRA_FLAGS) $(STATICCHECK_EXTRA_TARGETS) 2>&1 \
			| awk -v pwd="$$PWD/" -v cwd="$(CURDIR)/" '"'"'{ if (index($$0, pwd)==1) $$0=substr($$0, length(pwd)+1); if (index($$0, cwd)==1) $$0=substr($$0, length(cwd)+1); while (index($$0, "../")==1) $$0=substr($$0, 4); print }'"'"' | filter | sort > .make/staticcheck-extra.out || true; \
		if [ ! -f "$(STATICCHECK_EXTRA_BASELINE)" ]; then \
			touch "$(STATICCHECK_EXTRA_BASELINE)"; \
		fi; \
		tab=$$(printf "\t"); \
		metadata_prefix="$${tab}# staticcheck-extra:"; \
		baseline_findings() { \
			while IFS= read -r baseline_line || [ -n "$$baseline_line" ]; do \
				case "$$baseline_line" in ""|\#*) continue ;; esac; \
				finding="$${baseline_line%%$${metadata_prefix}*}"; \
				[ -n "$$finding" ] && printf "%s\n" "$$finding"; \
			done < "$(STATICCHECK_EXTRA_BASELINE)" | filter | sort; \
		}; \
		baseline_findings > .make/staticcheck-extra.baseline.out; \
		keyize() { awk '"'"'{ if (match($$0, /:[0-9]+:[0-9]+:/)) out=substr($$0, 1, RSTART-1) ":::" substr($$0, RSTART+RLENGTH); else out=$$0; while (index(out, "../")==1) out=substr(out, 4); print out }'"'"' "$$1"; }; \
		keyize .make/staticcheck-extra.out | sort -u > .make/staticcheck-extra.keys.out; \
		keyize .make/staticcheck-extra.baseline.out | sort -u > .make/staticcheck-extra.keys.baseline.out; \
		comm -23 .make/staticcheck-extra.keys.out .make/staticcheck-extra.keys.baseline.out > .make/staticcheck-extra.keys.new || true; \
		comm -13 .make/staticcheck-extra.keys.out .make/staticcheck-extra.keys.baseline.out > .make/staticcheck-extra.keys.gone || true; \
		map_keys() { awk '"'"'NR==FNR{keyset[$$0]=1; next} { if (match($$0, /:[0-9]+:[0-9]+:/)) k = substr($$0, 1, RSTART-1) ":::" substr($$0, RSTART+RLENGTH); else k = $$0; while (index(k, "../")==1) k = substr(k, 4); if (k in keyset) print }'"'"' "$$1" "$$2"; }; \
		new=$$(map_keys .make/staticcheck-extra.keys.new .make/staticcheck-extra.out); \
		if [ -n "$$new" ]; then \
			echo "NEW staticcheck-extra findings:"; \
			echo "$$new"; \
			echo ""; \
			echo "Fix these findings in code. Do not disable, silence, weaken, or otherwise circumvent the checks."; \
			exit 1; \
		fi; \
		gone=$$(map_keys .make/staticcheck-extra.keys.gone .make/staticcheck-extra.baseline.out); \
		if [ -n "$$gone" ]; then \
			echo "RESOLVED staticcheck-extra findings:"; \
			echo "$$gone"; \
		fi; \
		n=$$(wc -l < .make/staticcheck-extra.out); \
		echo "staticcheck-extra: OK ($$n findings)"'

staticcheck-extra-baseline: staticcheck-extra-bin
	@bash -eu -o pipefail -c '\
		bin="$(STATICCHECK_EXTRA_BIN)"; \
		[ -z "$$bin" ] && [ -x .make/staticcheck-extra ] && bin=".make/staticcheck-extra"; \
		if [ -z "$$bin" ] || [ ! -x "$$bin" ]; then \
			echo "staticcheck-extra: not configured; cannot refresh baseline"; exit 1; \
		fi; \
		mkdir -p .make "$$(dirname "$(STATICCHECK_EXTRA_BASELINE)")"; \
		excludes="$$(printf "%s,%s" "$(STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS)" "$(STATICCHECK_EXTRA_EXCLUDE_PATHS)")"; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat" || true; fi; \
		}; \
		"$$bin" $(STATICCHECK_EXTRA_FLAGS) $(STATICCHECK_EXTRA_TARGETS) 2>&1 \
			| awk -v pwd="$$PWD/" -v cwd="$(CURDIR)/" '"'"'{ if (index($$0, pwd)==1) $$0=substr($$0, length(pwd)+1); if (index($$0, cwd)==1) $$0=substr($$0, length(cwd)+1); while (index($$0, "../")==1) $$0=substr($$0, 4); print }'"'"' | filter | sort > .make/staticcheck-extra.out || true; \
		if [ ! -f "$(STATICCHECK_EXTRA_BASELINE)" ]; then \
			touch "$(STATICCHECK_EXTRA_BASELINE)"; \
		fi; \
		now=$$(date -u +"%Y-%m-%dT%H:%M:%SZ"); \
		tab=$$(printf "\t"); \
		metadata_prefix="$${tab}# staticcheck-extra:"; \
		tmp=".make/staticcheck-extra-baseline.tmp"; \
		printf "# staticcheck-extra: generated_at=%s\n" "$$now" > "$$tmp"; \
		awk -v now="$$now" -v mp="$${metadata_prefix}" -v lname=staticcheck-extra -v kmf="$(STATICCHECK_EXTRA_BASELINE)" '"'"'function key(s,    out) { if (match(s, /:[0-9]+:[0-9]+:/)) out = substr(s, 1, RSTART-1) ":::" substr(s, RSTART+RLENGTH); else out = s; while (index(out, "../")==1) out = substr(out, 4); return out } BEGIN { while ((getline line < kmf) > 0) { if (line ~ /^#/) continue; if (line ~ /^[ \t]*$$/) continue; idx = index(line, mp); if (idx > 0) { finding = substr(line, 1, idx-1); meta = substr(line, idx + length(mp)); fa = ""; n = split(meta, ff, " "); for (i = 1; i <= n; i++) if (ff[i] ~ /^first_added=/) fa = substr(ff[i], 13); km[key(finding)] = fa } else km[key(line)] = "" } close(kmf) } { k = key($$0); fa = (k in km) ? km[k] : ""; if (fa == "") fa = now; printf "%s\t# %s:first_added=%s last_seen=%s\n", $$0, lname, fa, now }'"'"' .make/staticcheck-extra.out >> "$$tmp"; \
		mv "$$tmp" "$(STATICCHECK_EXTRA_BASELINE)"; \
		n=$$(wc -l < .make/staticcheck-extra.out); \
		echo "staticcheck-extra: baseline $(STATICCHECK_EXTRA_BASELINE) refreshed ($$n findings)"'

# release/release-snapshot/release-local live in go-release.mk.
# Project Makefiles opt in via:  GO_MK_MODULES += go-release.mk

# smoke-fetch exercises the network fetch path end-to-end, even when the
# local-development override (GO_MK_DEV_DIR) is set in the shell. It clears
# the per-repo .make cache, runs make help with GO_MK_DEV_DIR forced empty
# inside the recursion, and confirms that go.mk plus every sibling module
# plus the central golangci.yml all download cleanly via the 3-tier
# (API, cache-busted raw, raw) chain.
#
# Useful before pushing a go-makefile change so a developer can verify the
# version on main is still working through the curl path that consumers
# actually take when GO_MK_DEV_DIR is unset.
.PHONY: smoke-fetch
smoke-fetch:
	@rm -rf .make
	@GO_MK_DEV_DIR= $(MAKE) --no-print-directory help >/dev/null
	@echo "smoke-fetch: OK ($$(ls .make 2>/dev/null | wc -l | tr -d ' ') assets fetched into .make/)"

# Refresh go.mk plus every opt-in sibling module and the central golangci.yml.
# Renamed from 'sync' to avoid conflicts with project-level Makefile sync targets.
# Uses the same 3-tier (API, cache-busted raw, raw) chain as go-mk-fetch-one
# so a freshly pushed update lands without waiting for raw-CDN invalidation,
# and curl stderr stays silent on retry-and-succeed cases.
update-go-mk go-mk-sync:
	@mkdir -p "$(dir $(GO_MK_CACHE))" "$(GO_MK_CACHE_DIR)"
	@for f in go.mk golangci.yml $(GO_MK_MODULES); do \
		api_url="$(GO_MK_API_BASE)/$$f?ref=$(GO_MK_API_REF)"; \
		raw_url="$(GO_MK_BASE_URL)/$$f"; \
		if [ "$$f" = "go.mk" ]; then dest="$(GO_MK)"; else dest=".make/$$f"; fi; \
		mkdir -p "$$(dirname $$dest)"; \
		if curl -fsSL -H "Accept: application/vnd.github.raw" --connect-timeout 5 --max-time 10 "$$api_url" -o "$$dest" 2>/dev/null \
			|| curl -fsSL --connect-timeout 5 --max-time 10 "$$raw_url?v=$$(date +%s)" -o "$$dest" 2>/dev/null \
			|| curl -fsSL --connect-timeout 5 --max-time 10 "$$raw_url" -o "$$dest" 2>/dev/null; then \
			cp "$$dest" "$(GO_MK_CACHE_DIR)/$$f"; \
			echo "updated: $$f"; \
		else \
			echo "error: $$f fetch failed" >&2; \
			exit 1; \
		fi; \
	done

# Include opt-in modules at end so they see all go.mk definitions
# (e.g., default-build-deps).
$(foreach m,$(GO_MK_MODULES),$(eval -include .make/$(m)))
