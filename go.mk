.PHONY: lint fmt vet test govulncheck check release go-mk-sync \
	staticcheck-extra staticcheck-extra-baseline staticcheck-extra-bin

GO_MK_URL   := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK_CACHE := $(HOME)/.cache/go-makefile/go.mk

GOLANGCI_LINT ?= golangci-lint
GOFUMPT       ?= gofumpt
GOIMPORTS     ?= goimports

ifndef CMD
.PHONY: build
build:
	go build ./...
endif

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	$(GOFUMPT) -w .
	$(GOIMPORTS) -w .

vet:
	go vet ./...

test:
	go test ./...

govulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

check: build vet lint test govulncheck staticcheck-extra

# ---------------------------------------------------------------------------
# staticcheck-extra: pluggable hook for an external analyzer binary
# (e.g. clyde-staticcheck) with a baseline-diff gate so only NEW findings
# fail the build. Disabled unless the project sets at least one of:
#
#   STATICCHECK_EXTRA_BIN              (path to a built analyzer binary)
#   STATICCHECK_EXTRA_BUILD_REPO       (path to a local Go repo) +
#   STATICCHECK_EXTRA_BUILD_PKG        (subpackage to `go build`)
#
# Optional knobs:
#   STATICCHECK_EXTRA_FLAGS         args passed to the analyzer (default empty)
#   STATICCHECK_EXTRA_TARGETS       packages to analyze         (default ./...)
#   STATICCHECK_EXTRA_BASELINE      baseline file               (default .staticcheck-extra-baseline.txt)
#   STATICCHECK_EXTRA_EXCLUDE_PATHS comma-separated grep -E patterns matched
#                                   against finding lines (file:line:col:msg).
#                                   Any line that matches is dropped before
#                                   the baseline diff. Use for generated code
#                                   (e.g. "\.pb\.go:") or vendored paths.
#
# Behaviour:
#   - If neither BIN nor BUILD_REPO set, target is a no-op (announces "skipped").
#   - If BUILD_REPO+BUILD_PKG set, the analyzer is built into .make/ on demand.
#   - Excluded lines are dropped before baseline comparison.
#   - Findings are diffed against the baseline. NEW findings exit non-zero.
#     RESOLVED findings print a hint to refresh the baseline but don't fail.
#   - `make staticcheck-extra-baseline` re-captures the current findings.
# ---------------------------------------------------------------------------
STATICCHECK_EXTRA_BIN           ?=
STATICCHECK_EXTRA_BUILD_REPO    ?=
STATICCHECK_EXTRA_BUILD_PKG     ?=
STATICCHECK_EXTRA_FLAGS         ?=
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
		if [ -n "$$bin" ]; then \
			[ -x "$$bin" ] || { echo "staticcheck-extra: $$bin not executable"; exit 1; }; \
			exit 0; \
		fi; \
		if [ -z "$$repo" ]; then exit 0; fi; \
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
		new=$$(comm -23 .make/staticcheck-extra.out "$(STATICCHECK_EXTRA_BASELINE)" || true); \
		if [ -n "$$new" ]; then \
			echo "NEW staticcheck-extra findings (not in baseline):"; \
			echo "$$new"; \
			echo ""; \
			echo "Either fix them, or refresh the baseline:"; \
			echo "  make staticcheck-extra-baseline"; \
			exit 1; \
		fi; \
		gone=$$(comm -13 .make/staticcheck-extra.out "$(STATICCHECK_EXTRA_BASELINE)" || true); \
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
		excludes="$(STATICCHECK_EXTRA_EXCLUDE_PATHS)"; \
		filter() { \
			if [ -z "$$excludes" ]; then cat; return; fi; \
			pat=$$(printf "%s" "$$excludes" | tr "," "\n" | grep -v "^$$" | paste -sd "|" -); \
			if [ -z "$$pat" ]; then cat; else grep -Ev "$$pat"; fi; \
		}; \
		"$$bin" $(STATICCHECK_EXTRA_FLAGS) $(STATICCHECK_EXTRA_TARGETS) 2>&1 \
			| sed "s|$(CURDIR)/||g" | filter | sort > "$(STATICCHECK_EXTRA_BASELINE)"; \
		n=$$(wc -l < "$(STATICCHECK_EXTRA_BASELINE)"); \
		echo "staticcheck-extra: baseline $(STATICCHECK_EXTRA_BASELINE) refreshed ($$n findings)"'

# Local release with notarization. Requires notarize.env (gitignored).
# Copy notarize.env.example to notarize.env and fill in your 1Password paths.
release:
	@[ -f notarize.env ] || { echo "notarize.env not found. Copy notarize.env.example and fill in your 1Password op:// paths."; exit 1; }
	op run --env-file=notarize.env -- goreleaser release --clean

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
