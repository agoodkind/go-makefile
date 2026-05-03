.PHONY: lint lint-tools lint-golangci lint-format fmt vet test govulncheck check \
	staticcheck-extra staticcheck-extra-baseline staticcheck-extra-bin \
	release go-mk-sync

GO_MK_URL   := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK_CACHE := $(HOME)/.cache/go-makefile/go.mk

GOLANGCI_LINT         ?= golangci-lint
GOFUMPT               ?= gofumpt
GOIMPORTS             ?= goimports
GOLANGCI_LINT_INSTALL ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
GOFUMPT_INSTALL       ?= mvdan.cc/gofumpt@v0.9.2
GOIMPORTS_INSTALL     ?= golang.org/x/tools/cmd/goimports@v0.44.0

ifndef CMD
.PHONY: build
build:
	go build ./...
endif

lint: lint-tools lint-golangci lint-format staticcheck-extra

lint-tools:
	go install $(GOLANGCI_LINT_INSTALL)
	go install $(GOFUMPT_INSTALL)
	go install $(GOIMPORTS_INSTALL)

lint-golangci:
	$(GOLANGCI_LINT) run ./...

lint-format:
	@diff_output=$$($(GOLANGCI_LINT) fmt --diff ./...); \
	if [ -n "$$diff_output" ]; then \
		echo "golangci-lint formatters need to update:"; \
		printf '%s\n' "$$diff_output"; \
		echo "run make fmt"; \
		exit 1; \
	fi

fmt: lint-tools
	$(GOLANGCI_LINT) fmt ./...

vet:
	go vet ./...

test:
	go test ./...

govulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

check: build vet lint test govulncheck

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
#                                   enables all 5 bundled checks)
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
#     into $(go env GOPATH)/bin on first run, then cached.
#   - Excluded lines are dropped before baseline comparison.
#   - Findings are diffed against the baseline. NEW findings exit non-zero.
#     RESOLVED findings print a hint to refresh the baseline but don't fail.
#   - `make staticcheck-extra-baseline` re-captures the current findings and
#     records generated_at for the file plus first_added and last_seen UTC
#     timestamps for each entry.
# ---------------------------------------------------------------------------
STATICCHECK_EXTRA_BIN           ?=
STATICCHECK_EXTRA_BUILD_REPO    ?=
STATICCHECK_EXTRA_BUILD_PKG     ?=
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
	-silent_defer_close \
	-slog_missing_trace_id \
	-grpc_handler_missing_peer_enrichment \
	-sensitive_field_in_log
STATICCHECK_EXTRA_FLAGS         ?= $(STATICCHECK_EXTRA_CORE_FLAGS)
STATICCHECK_EXTRA_TARGETS       ?= ./...
STATICCHECK_EXTRA_BASELINE      ?= .staticcheck-extra-baseline.txt
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
		if [ -n "$$bin" ]; then \
			[ -x "$$bin" ] || { echo "staticcheck-extra: $$bin not executable"; exit 1; }; \
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
			if [ ! -x "$$out" ] || [ -n "$$newest_src" ]; then \
				cd "$$repo" && go build -o "$$out" "$$pkg"; \
			fi; \
			exit 0; \
		fi; \
		if [ -z "$$install" ]; then exit 0; fi; \
		mkdir -p .make; \
		out="$(CURDIR)/.make/staticcheck-extra"; \
		base=$$(basename "$$install" | sed "s/@.*//"); \
		gobin=$$(go env GOPATH)/bin; \
		installed="$$gobin/$$base"; \
		if [ ! -x "$$installed" ]; then \
			GOBIN="$$gobin" go install "$$install"; \
		fi; \
		ln -sf "$$installed" "$$out"'

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
		excludes="$(STATICCHECK_EXTRA_EXCLUDE_PATHS)"; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat"; fi; \
		}; \
		"$$bin" $(STATICCHECK_EXTRA_FLAGS) $(STATICCHECK_EXTRA_TARGETS) 2>&1 \
			| sed "s|$(CURDIR)/||g" | filter | sort > .make/staticcheck-extra.out || true; \
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
			done < "$(STATICCHECK_EXTRA_BASELINE)" | sort; \
		}; \
		baseline_findings > .make/staticcheck-extra.baseline.out; \
		new=$$(comm -23 .make/staticcheck-extra.out .make/staticcheck-extra.baseline.out || true); \
		if [ -n "$$new" ]; then \
			echo "NEW staticcheck-extra findings (not in baseline):"; \
			echo "$$new"; \
			echo ""; \
			echo "Either fix them, or refresh the baseline:"; \
			echo "  make staticcheck-extra-baseline"; \
			exit 1; \
		fi; \
		gone=$$(comm -13 .make/staticcheck-extra.out .make/staticcheck-extra.baseline.out || true); \
		if [ -n "$$gone" ]; then \
			echo "RESOLVED staticcheck-extra findings (please refresh baseline):"; \
			echo "$$gone"; \
		fi; \
		n=$$(wc -l < .make/staticcheck-extra.out); \
		echo "staticcheck-extra: OK ($$n findings, all in baseline)"'

staticcheck-extra-baseline: staticcheck-extra-bin
	@bash -eu -o pipefail -c '\
		bin="$(STATICCHECK_EXTRA_BIN)"; \
		[ -z "$$bin" ] && [ -x .make/staticcheck-extra ] && bin=".make/staticcheck-extra"; \
		if [ -z "$$bin" ] || [ ! -x "$$bin" ]; then \
			echo "staticcheck-extra: not configured; cannot refresh baseline"; exit 1; \
		fi; \
		mkdir -p .make "$$(dirname "$(STATICCHECK_EXTRA_BASELINE)")"; \
		excludes="$(STATICCHECK_EXTRA_EXCLUDE_PATHS)"; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat"; fi; \
		}; \
		"$$bin" $(STATICCHECK_EXTRA_FLAGS) $(STATICCHECK_EXTRA_TARGETS) 2>&1 \
			| sed "s|$(CURDIR)/||g" | filter | sort > .make/staticcheck-extra.out || true; \
		if [ ! -f "$(STATICCHECK_EXTRA_BASELINE)" ]; then \
			touch "$(STATICCHECK_EXTRA_BASELINE)"; \
		fi; \
		now=$$(date -u +"%Y-%m-%dT%H:%M:%SZ"); \
		tab=$$(printf "\t"); \
		metadata_prefix="$${tab}# staticcheck-extra:"; \
		tmp=".make/staticcheck-extra-baseline.tmp"; \
		printf "# staticcheck-extra: generated_at=%s\n" "$$now" > "$$tmp"; \
		while IFS= read -r finding || [ -n "$$finding" ]; do \
			first_added=""; \
			while IFS= read -r baseline_line || [ -n "$$baseline_line" ]; do \
				case "$$baseline_line" in ""|\#*) continue ;; esac; \
				baseline_finding="$${baseline_line%%$${metadata_prefix}*}"; \
				[ "$$baseline_finding" = "$$finding" ] || continue; \
				metadata="$${baseline_line#*$${metadata_prefix}}"; \
				if [ "$$metadata" != "$$baseline_line" ]; then \
					for metadata_field in $$metadata; do \
						case "$$metadata_field" in first_added=*) first_added="$${metadata_field#first_added=}" ;; esac; \
					done; \
				fi; \
				break; \
			done < "$(STATICCHECK_EXTRA_BASELINE)"; \
			if [ -z "$$first_added" ]; then \
				first_added="$$now"; \
			fi; \
			printf "%s\t# staticcheck-extra:first_added=%s last_seen=%s\n" "$$finding" "$$first_added" "$$now" >> "$$tmp"; \
		done < .make/staticcheck-extra.out; \
		mv "$$tmp" "$(STATICCHECK_EXTRA_BASELINE)"; \
		n=$$(wc -l < .make/staticcheck-extra.out); \
		echo "staticcheck-extra: baseline $(STATICCHECK_EXTRA_BASELINE) refreshed ($$n findings)"'

# Local release with notarization. Requires notarize.env (gitignored).
# Copy notarize.env.example to notarize.env and fill in your 1Password paths.
release:
	@[ -f notarize.env ] || { echo "notarize.env not found. Copy notarize.env.example and fill in your 1Password op:// paths."; exit 1; }
	op run --env-file=notarize.env "$$(printf '%s%s' - -)" goreleaser release --clean

# Renamed from 'sync' to avoid conflicts with project-level Makefile sync targets.
go-mk-sync:
	@mkdir -p "$(dir $(GO_MK_CACHE))"
	@if curl -fsSL --connect-timeout 5 --max-time 10 "$(GO_MK_URL)" -o "$(GO_MK)"; then \
		cp "$(GO_MK)" "$(GO_MK_CACHE)"; \
		echo "go.mk updated"; \
	else \
		echo "error: go.mk fetch failed" >&2; \
		exit 1; \
	fi
