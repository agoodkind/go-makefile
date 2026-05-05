.PHONY: build deploy clean help \
	lint lint-tools lint-golangci lint-golangci-baseline lint-format lint-gocyclo fmt vet test govulncheck build-check check \
	staticcheck-extra staticcheck-extra-baseline staticcheck-extra-bin \
	go-mk-sync update-go-mk

GO_MK_URL       := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK_CACHE     := $(HOME)/.cache/go-makefile/go.mk
GO_MK_BASE_URL  ?= https://raw.githubusercontent.com/agoodkind/go-makefile/main
GO_MK_CACHE_DIR ?= $(or $(XDG_CACHE_HOME),$(HOME)/.cache)/go-makefile

# go-mk-fetch-one: fetch one asset from go-makefile (relative path, e.g.
# go-build.mk or golangci.yml) into .make/<path>. Honors GO_MK_DEV_DIR for
# local dev iteration. Falls back to ~/.cache/go-makefile/ on network error.
# Used by the GO_MK_MODULES bootstrap and the golangci config fetch below.
# All output goes to stderr; $(call ...) evaluates to the empty string so
# it's safe to use at the top level.
go-mk-fetch-one = $(shell { \
	mkdir -p .make "$(GO_MK_CACHE_DIR)"; \
	target=".make/$(1)"; \
	cache="$(GO_MK_CACHE_DIR)/$(1)"; \
	if [ -n "$(GO_MK_DEV_DIR)" ] && [ -f "$(GO_MK_DEV_DIR)/$(1)" ]; then \
		cp "$(GO_MK_DEV_DIR)/$(1)" "$$target"; \
		printf '%s\n' "$(1): using dev override $(GO_MK_DEV_DIR)/$(1)"; \
	else \
		tmp="$$target.tmp"; \
		if curl -fsSL --connect-timeout 5 --max-time 10 "$(GO_MK_BASE_URL)/$(1)" -o "$$tmp" 2>/dev/null; then \
			mv "$$tmp" "$$target"; cp "$$target" "$$cache"; \
		elif [ -f "$$cache" ]; then \
			rm -f "$$tmp"; cp "$$cache" "$$target"; \
			printf '%s\n' "warning: $(1) fetch failed, using cached version"; \
		elif [ ! -f "$$target" ]; then \
			rm -f "$$tmp"; \
			printf '%s\n' "error: $(1) fetch failed and no cache available"; \
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
GOLANGCI_LINT_FLAGS    ?= -c $(GO_MK_GOLANGCI_CONFIG)
GOLANGCI_LINT_BASELINE ?= .golangci-lint-baseline.txt
GOLANGCI_LINT_BASELINE_RUNS ?= 3
GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS ?= _test\.go:
GOLANGCI_LINT_EXCLUDE_PATHS ?=
GOFUMPT                ?= gofumpt
GOIMPORTS              ?= goimports
GOCYCLO_OVER           ?= 50
GOCYCLO_TARGETS        ?= $$(find . -name '*.go' -not -name '*_test.go' -not -path './vendor/*' -not -path './gen/*' -not -path './third_party/*')
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
	@printf '%s\n' 'Common targets:'
	@printf '  %-28s %s\n' 'build' 'run build-check, then go build $$(GO_BUILD_TARGETS)'
	@printf '  %-28s %s\n' 'test' 'go test $$(GO_TEST_TARGETS)'
	@printf '  %-28s %s\n' 'lint' 'install lint tools and run all lint gates'
	@printf '  %-28s %s\n' 'build-check' 'run vet, lint, and govulncheck'
	@printf '  %-28s %s\n' 'check' 'run build, then test'
	@printf '  %-28s %s\n' 'fmt' 'apply configured golangci formatters'
	@printf '  %-28s %s\n' 'deploy' 'go install $$(GO_INSTALL_TARGET)'
	@printf '  %-28s %s\n' 'go-mk-sync/update-go-mk' 'refresh $$(GO_MK) and $$(GO_MK_CACHE)'

lint: lint-tools lint-golangci lint-format lint-gocyclo staticcheck-extra

lint-tools:
	go install $(GOLANGCI_LINT_INSTALL)
	go install $(GOFUMPT_INSTALL)
	go install $(GOIMPORTS_INSTALL)

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
			| sed "s|$(CURDIR)/||g" \
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
		new=$$(comm -23 "$$findings_output" "$$baseline_output" || true); \
		if [ -n "$$new" ]; then \
			echo "NEW golangci-lint findings:"; \
			echo "$$new"; \
			echo ""; \
			echo "Fix these findings in code. Do not disable, silence, weaken, or otherwise circumvent the checks."; \
			exit 1; \
		fi; \
		gone=$$(comm -13 "$$findings_output" "$$baseline_output" || true); \
		if [ -n "$$gone" ]; then \
			echo "RESOLVED golangci-lint findings:"; \
			echo "$$gone"; \
		fi; \
		n=$$(wc -l < "$$findings_output"); \
		echo "golangci-lint: OK ($$n findings)"; \
		if [ "$$status" -ne 0 ] && [ ! -s "$$findings_output" ]; then cat "$$raw_output"; exit "$$status"; fi'

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
				| sed "s|$(CURDIR)/||g" \
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
		sort -u "$$findings_output" "$$baseline_output" > "$$findings_output.merged"; \
		mv "$$findings_output.merged" "$$findings_output"; \
		printf "# golangci-lint: generated_at=%s\n" "$$now" > "$$new_baseline"; \
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
			done < "$(GOLANGCI_LINT_BASELINE)"; \
			if [ -z "$$first_added" ]; then first_added="$$now"; fi; \
			printf "%s\t# golangci-lint:first_added=%s last_seen=%s\n" "$$finding" "$$first_added" "$$now" >> "$$new_baseline"; \
		done < "$$findings_output"; \
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
	go tool gocyclo -over $(GOCYCLO_OVER) $(GOCYCLO_TARGETS)

fmt: lint-tools
	$(GOLANGCI_LINT) fmt $(GOLANGCI_LINT_FLAGS) $(GOLANGCI_LINT_TARGETS)

vet:
	go vet $(GO_VET_TARGETS)

test:
	go test $(GO_TEST_TARGETS)

govulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck $(GOVULNCHECK_TARGETS)

build-check: vet lint govulncheck

check: build test

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
	-sensitive_field_in_log \
	-nolint_ban
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
			base=$$(basename "$$install" | sed "s/@.*//"); \
			gobin=$$(go env GOPATH)/bin; \
			installed="$$gobin/$$base"; \
			GOPROXY=direct GONOSUMDB=github.com/agoodkind/go-makefile,github.com/agoodkind/go-makefile/staticcheck GOBIN="$$gobin" go install "$$install"; \
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
		base=$$(basename "$$install" | sed "s/@.*//"); \
		gobin=$$(go env GOPATH)/bin; \
		installed="$$gobin/$$base"; \
		if [ ! -x "$$installed" ] || missing_flags "$$installed"; then \
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
			done < "$(STATICCHECK_EXTRA_BASELINE)" | filter | sort; \
		}; \
		baseline_findings > .make/staticcheck-extra.baseline.out; \
		new=$$(comm -23 .make/staticcheck-extra.out .make/staticcheck-extra.baseline.out || true); \
		if [ -n "$$new" ]; then \
			echo "NEW staticcheck-extra findings:"; \
			echo "$$new"; \
			echo ""; \
			echo "Fix these findings in code. Do not disable, silence, weaken, or otherwise circumvent the checks."; \
			exit 1; \
		fi; \
		gone=$$(comm -13 .make/staticcheck-extra.out .make/staticcheck-extra.baseline.out || true); \
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

# release/release-snapshot/release-local live in go-release.mk.
# Project Makefiles opt in via:  GO_MK_MODULES += go-release.mk

# Refresh go.mk plus every opt-in sibling module and the central golangci.yml.
# Renamed from 'sync' to avoid conflicts with project-level Makefile sync targets.
update-go-mk go-mk-sync:
	@mkdir -p "$(dir $(GO_MK_CACHE))" "$(GO_MK_CACHE_DIR)"
	@for f in go.mk golangci.yml $(GO_MK_MODULES); do \
		url="$(GO_MK_BASE_URL)/$$f"; \
		if [ "$$f" = "go.mk" ]; then dest="$(GO_MK)"; else dest=".make/$$f"; fi; \
		mkdir -p "$$(dirname $$dest)"; \
		if curl -fsSL --connect-timeout 5 --max-time 10 "$$url" -o "$$dest"; then \
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
