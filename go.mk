.PHONY: build deploy clean help \
	lint lint-tools lint-golangci lint-golangci-baseline lint-golangci-baseline-prune-fixed lint-golangci-baseline-remove-fixed lint-golangci-baseline-accept-new \
	lint-golangci-scope lint-golangci-baseline-scope lint-golangci-baseline-scope-accept-new \
	lint-files lint-diff lint-format lint-gocyclo lint-gocyclo-baseline lint-gocyclo-baseline-prune-fixed lint-gocyclo-baseline-remove-fixed lint-gocyclo-baseline-accept-new fmt vet test govulncheck build-gate build-check check \
	lint-deadcode lint-deadcode-baseline lint-deadcode-baseline-prune-fixed lint-deadcode-baseline-remove-fixed lint-deadcode-baseline-accept-new \
	staticcheck-extra staticcheck-extra-baseline staticcheck-extra-baseline-prune-fixed staticcheck-extra-baseline-remove-fixed staticcheck-extra-baseline-accept-new staticcheck-extra-bin \
	baseline baseline-bin baseline-prune-fixed baseline-remove-fixed baseline-accept-new baseline-add-new \
	go-mk-sync update-go-mk smoke-fetch go-mk-notice go-version-check go-mk-bin

GO_MK_URL       := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK_CACHE     := $(HOME)/.cache/go-makefile/go.mk
GO_MK_BASE_URL  ?= https://raw.githubusercontent.com/agoodkind/go-makefile/main
GO_MK_API_REPO  ?= agoodkind/go-makefile
GO_MK_API_REF   ?= main
GO_MK_CACHE_DIR ?= $(or $(XDG_CACHE_HOME),$(HOME)/.cache)/go-makefile

GO_MK_SELF      := $(lastword $(MAKEFILE_LIST))
GO_MK_SELF_DIR  := $(patsubst %/,%,$(dir $(abspath $(GO_MK_SELF))))
GO_MK_LOCAL_SCRIPT_DIR := $(if $(strip $(GO_MK_DEV_DIR)),$(GO_MK_DEV_DIR)/scripts,$(GO_MK_SELF_DIR)/scripts)
GO_MK_FETCHED_SCRIPT_DIR := $(CURDIR)/.make/scripts
GO_MK_HELPER_DIR := $(if $(wildcard $(GO_MK_LOCAL_SCRIPT_DIR)/go-mk-bin.sh),$(GO_MK_LOCAL_SCRIPT_DIR),$(GO_MK_FETCHED_SCRIPT_DIR))
GO_MK_FETCH_SCRIPT := $(GO_MK_HELPER_DIR)/go-mk-fetch-one.sh
GO_MK_LOCAL_NOTICES := $(if $(strip $(GO_MK_DEV_DIR)),$(GO_MK_DEV_DIR)/notices.txt,$(GO_MK_SELF_DIR)/notices.txt)
GO_MK_NOTICES_FILE := $(if $(wildcard $(GO_MK_LOCAL_NOTICES)),$(GO_MK_LOCAL_NOTICES),$(CURDIR)/.make/notices.txt)

# go.mk still contains this small bootstrap fetcher because old consumers only
# fetch go.mk first. Once helper scripts are present, every larger shell and
# awk body lives under scripts/.
define _go_mk_fetch_bootstrap_commands
	mkdir -p "$$(dirname "$(2)")"; \
	tmp=$$(mktemp "$(2).tmp.XXXXXX") || exit 1; \
	err=$$(mktemp "$(2).err.XXXXXX") || { rm -f "$$tmp"; exit 1; }; \
	if [ -n "$(3)" ] && [ -f "$(3)/$(1)" ]; then \
		cp "$(3)/$(1)" "$$tmp" || { rm -f "$$tmp" "$$err"; exit 1; }; \
	else \
		gh_path=$$(command -v gh || true); \
		if [ -n "$$gh_path" ] && gh api "repos/$(GO_MK_API_REPO)/contents/$(1)?ref=$(GO_MK_API_REF)" -H "Accept: application/vnd.github.raw" > "$$tmp" 2>"$$err"; then \
			:; \
		elif curl -fsSL --connect-timeout 5 --max-time 10 "$(GO_MK_BASE_URL)/$(1)?v=$$(date +%s)" -o "$$tmp" 2>"$$err"; then \
			:; \
		elif curl -fsSL --connect-timeout 5 --max-time 10 "$(GO_MK_BASE_URL)/$(1)" -o "$$tmp" 2>"$$err"; then \
			:; \
		else \
			rm -f "$$tmp" "$$err"; \
			exit 1; \
		fi; \
	fi; \
	if [ -s "$$tmp" ]; then \
		mv "$$tmp" "$(2)"; \
		case "$(2)" in *.sh) chmod +x "$(2)" ;; esac; \
		rm -f "$$err"; \
	else \
		rm -f "$$tmp" "$$err"; \
		exit 1; \
	fi
endef

define go_mk_fetch_bootstrap
$(shell mkdir -p .make && $(call _go_mk_fetch_bootstrap_commands,$(1),$(2),$(GO_MK_DEV_DIR)) > .make/go-mk-bootstrap-fetch.log)
$(if $(wildcard $(2)),,$(error go-makefile failed to fetch $(1) into $(2)))
endef

ifeq ($(GO_MK_HELPER_DIR),$(GO_MK_FETCHED_SCRIPT_DIR))
GO_MK_FETCHED_BOOTSTRAP := $(call go_mk_fetch_bootstrap,scripts/go-mk-fetch-one.sh,.make/scripts/go-mk-fetch-one.sh)
endif

define go-mk-fetch-one
$(if $(filter ok,$(shell mkdir -p .make && bash "$(GO_MK_FETCH_SCRIPT)" "$(1)" ".make/$(1)" "$(GO_MK_DEV_DIR)" > .make/go-mk-fetch.log 2>&1; test -s ".make/$(1)" && echo ok)),,$(error go-makefile failed to fetch $(1)))
endef

define go-mk-require-one
$(if $(wildcard $(1)),,$(error go-makefile expected $(1); rerun without GO_MK_SKIP_FETCH))
endef

GO_MK_SCRIPT_FILES := \
	scripts/go-mk-fetch-one.sh \
	scripts/go-mk-bin.sh \
	scripts/go-mk-sync.sh \
	notices.txt

ifeq ($(GO_MK_HELPER_DIR),$(GO_MK_FETCHED_SCRIPT_DIR))
ifeq ($(strip $(GO_MK_SKIP_FETCH)),1)
GO_MK_FETCHED_SCRIPTS := $(foreach s,$(GO_MK_SCRIPT_FILES),$(call go-mk-require-one,.make/$(s)))
else
GO_MK_FETCHED_SCRIPTS := $(foreach s,$(GO_MK_SCRIPT_FILES),$(call go-mk-fetch-one,$(s)))
endif
endif

# GO_MK_MODULES: project sets a list of sibling .mk files to fetch and include.
# Example: GO_MK_MODULES := go-build.mk go-release.mk go-service.mk
GO_MK_MODULES ?=
ifneq ($(strip $(GO_MK_BOOTSTRAP_FETCHED)$(GO_MK_SKIP_FETCH)),)
GO_MK_FETCHED_MODULES := $(foreach m,$(GO_MK_MODULES),$(call go-mk-require-one,.make/$(m)))
else
GO_MK_FETCHED_MODULES := $(foreach m,$(GO_MK_MODULES),$(call go-mk-fetch-one,$(m)))
endif

# Centralized golangci-lint config. Consumers do not maintain their own copy.
GO_MK_GOLANGCI_CONFIG ?= .make/golangci.yml
ifneq ($(strip $(GO_MK_BOOTSTRAP_FETCHED)$(GO_MK_SKIP_FETCH)),)
GO_MK_FETCHED_GOLANGCI := $(call go-mk-require-one,$(GO_MK_GOLANGCI_CONFIG))
else
GO_MK_FETCHED_GOLANGCI := $(call go-mk-fetch-one,golangci.yml)
endif

GOLANGCI_LINT          ?= golangci-lint
GOLANGCI_LINT_TARGETS  ?= ./...
LINT_CONCURRENCY       ?= auto
GO_MK_COMMA            := ,
GOLANGCI_LINT_FLAGS    ?= -c $(GO_MK_GOLANGCI_CONFIG)
GOLANGCI_LINT_RUN_FLAGS ?= $(GOLANGCI_LINT_FLAGS) --allow-parallel-runners $(if $(filter-out 0 auto,$(strip $(LINT_CONCURRENCY))),--concurrency=$(LINT_CONCURRENCY))
GOLANGCI_LINT_BASELINE ?= .golangci-lint-baseline.txt
GOLANGCI_LINT_BASELINE_RUNS ?= 3
GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS ?= _test\.go:
GOLANGCI_LINT_EXCLUDE_PATHS ?=
GOLANGCI_LINT_BASELINE_SCOPE_PATTERN ?=
GOLANGCI_FMT_FILES     ?=
LINTER                 ?=
RULE                   ?=
GOFUMPT                ?= gofumpt
GOIMPORTS              ?= goimports
GOCYCLO_OVER           ?= 30
GOCYCLO_TARGETS        ?= $$(find . -name '*.go' -not -name '*_test.go' -not -path './vendor/*' -not -path './gen/*' -not -path './third_party/*')
GOCYCLO_INSTALL        ?= github.com/fzipp/gocyclo/cmd/gocyclo@latest
GOCYCLO_BASELINE       ?= .gocyclo-baseline.txt
GOCYCLO_DEFAULT_EXCLUDE_PATHS ?= _test\.go:
GOCYCLO_EXCLUDE_PATHS  ?=
GOLANGCI_LINT_INSTALL  ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
GOFUMPT_INSTALL        ?= mvdan.cc/gofumpt@v0.10.0
GOIMPORTS_INSTALL      ?= golang.org/x/tools/cmd/goimports@v0.45.0
GO_BUILD_OUTPUT        ?= $(if $(strip $(CMD)),$(BINARY),)
GO_BUILD_FLAGS         ?=
GO_BUILD_OUTPUT_FLAGS  ?= $(if $(strip $(GO_BUILD_OUTPUT)),-o $(GO_BUILD_OUTPUT),)
GO_BUILD_TARGETS       ?= $(if $(strip $(CMD)),$(CMD),./...)
GO_TEST_TARGETS        ?= ./...
GO_TEST_LDFLAGS        ?=
GO_VET_TARGETS         ?= ./...
GOVULNCHECK_TARGETS    ?= ./...
# Pinned to v1.3.0 (vendoring golang.org/x/tools v0.44.0). govulncheck v1.4.0
# bumped to x/tools v0.46.0, whose ssa RuntimeTypes panics on uninstantiated
# generic type params (golang/go#77549). Move the pin forward once a released
# x/tools carries the generic-methods fix.
GOVULNCHECK_INSTALL    ?= golang.org/x/vuln/cmd/govulncheck@v1.3.0
GO_INSTALL_FLAGS       ?= $(filter-out -o %,$(GO_BUILD_FLAGS))
GO_INSTALL_TARGET      ?= $(CMD)

# Gate tokens default to today's Wikipedia featured article slug, but can be
# swapped to any rotating public or private endpoint that emits one string.
GO_MK_GATE_TOKEN_CMD ?= curl -fsSL "https://en.wikipedia.org/api/rest_v1/feed/featured/$$(date -u +%Y/%m/%d)" | jq -r '.tfa.titles.canonical'

BASELINE_CONFIRM   ?=
BASELINE_TOKEN     ?=
BASELINE_TOKEN_CMD ?= $(GO_MK_GATE_TOKEN_CMD)
BASELINE_UPDATE_MODE ?= sync

LINT_GATES := lint-golangci lint-format lint-gocyclo lint-deadcode staticcheck-extra

# lint-deadcode runs golang.org/x/tools/cmd/deadcode and gates new findings
# against a baseline file.
DEADCODE_INSTALL          ?= golang.org/x/tools/cmd/deadcode@latest
DEADCODE_TARGETS          ?= ./...
DEADCODE_BASELINE         ?= .deadcode-baseline.txt
DEADCODE_DEFAULT_EXCLUDE_PATHS ?= _test\.go:
DEADCODE_EXCLUDE_PATHS    ?=

# staticcheck-extra: AST analyzer pass with a baseline-diff gate so only new
# findings fail the build.
STATICCHECK_EXTRA_BIN           ?=
STATICCHECK_EXTRA_BUILD_REPO    ?= $(if $(and $(GO_MK_DEV_DIR),$(wildcard $(GO_MK_DEV_DIR)/staticcheck/cmd/staticcheck-extra)),$(GO_MK_DEV_DIR)/staticcheck)
STATICCHECK_EXTRA_BUILD_PKG     ?= $(if $(STATICCHECK_EXTRA_BUILD_REPO),./cmd/staticcheck-extra)
STATICCHECK_EXTRA_INSTALL       ?= goodkind.io/go-makefile/staticcheck/cmd/staticcheck-extra@latest
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
	-thin_wrapper_to_launderable_call \
	-rta_throwaway_registration \
	-rta_synthetic_marker_call \
	-rta_slog_field_bypass \
	-lifecycle_noop_closer \
	-lifecycle_silent_close_err \
	-no_tilde_path_literal
STATICCHECK_EXTRA_FLAGS         ?= $(STATICCHECK_EXTRA_CORE_FLAGS) $(STATICCHECK_EXTRA_STRICT_FLAGS)
STATICCHECK_EXTRA_TARGETS       ?= ./...
STATICCHECK_EXTRA_BASELINE      ?= .staticcheck-extra-baseline.txt
STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS ?= _test\.go:
STATICCHECK_EXTRA_EXCLUDE_PATHS ?=

# go-mk engine binary, built on demand from this (root) module. The
# default install spec tracks the main branch tip (@main) so every consumer
# resolves the current engine with no version pin.
GO_MK_BIN          ?=
GO_MK_BUILD_REPO   ?= $(if $(and $(GO_MK_DEV_DIR),$(wildcard $(GO_MK_DEV_DIR)/cmd/go-mk)),$(GO_MK_DEV_DIR))
GO_MK_BUILD_PKG    ?= $(if $(GO_MK_BUILD_REPO),./cmd/go-mk)
GO_MK_INSTALL      ?= goodkind.io/go-makefile/cmd/go-mk@main

# Path to the resolved go-mk engine binary. go-mk-bin.sh prints the configured
# GO_MK_BIN or the on-demand .make/go-mk build output. The lint targets depend
# on the go-mk-bin target so the binary is built before they invoke it.
GO_MK_BIN_RESOLVED := $(if $(strip $(GO_MK_BIN)),$(GO_MK_BIN),$(CURDIR)/.make/go-mk)

export GO_MK_ROOT := $(CURDIR)
export GO_MK_HELPER_DIR
export GO_MK_NOTICES_FILE
export GO_MK_SCRIPT_FILES
export GO_MK_BASE_URL
export GO_MK_API_REPO
export GO_MK_API_REF
export GOLANGCI_LINT
export GOLANGCI_LINT_TARGETS
export GOLANGCI_LINT_FLAGS
export GOLANGCI_LINT_RUN_FLAGS
export GOLANGCI_LINT_BASELINE
export GOLANGCI_LINT_BASELINE_RUNS
export GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS
export GOLANGCI_LINT_EXCLUDE_PATHS
export GOLANGCI_LINT_BASELINE_SCOPE_PATTERN
export GOLANGCI_FMT_FILES
export LINTER
export RULE
export GOLANGCI_LINT_INSTALL
export GOFUMPT_INSTALL
export GOIMPORTS_INSTALL
export LINT_CONCURRENCY
export LINT_GATES
export LINT_FILES
export BASELINE
export BASELINE_CONFIRM
export BASELINE_TOKEN
export BASELINE_TOKEN_CMD
export BASELINE_UPDATE_MODE
export GO_MK_GATE_TOKEN_CMD
export GOCYCLO_OVER
export GOCYCLO_TARGETS
export GOCYCLO_INSTALL
export GOCYCLO_BASELINE
export GOCYCLO_DEFAULT_EXCLUDE_PATHS
export GOCYCLO_EXCLUDE_PATHS
export GO_TEST_TARGETS
export GO_TEST_LDFLAGS
export GO_VET_TARGETS
export GOVULNCHECK_TARGETS
export GOVULNCHECK_INSTALL
export DEADCODE_INSTALL
export DEADCODE_TARGETS
export DEADCODE_BASELINE
export DEADCODE_DEFAULT_EXCLUDE_PATHS
export DEADCODE_EXCLUDE_PATHS
export STATICCHECK_EXTRA_BIN
export STATICCHECK_EXTRA_BUILD_REPO
export STATICCHECK_EXTRA_BUILD_PKG
export STATICCHECK_EXTRA_INSTALL
export STATICCHECK_EXTRA_FLAGS
export STATICCHECK_EXTRA_TARGETS
export STATICCHECK_EXTRA_BASELINE
export STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS
export STATICCHECK_EXTRA_EXCLUDE_PATHS
export GO_MK_BIN
export GO_MK_BUILD_REPO
export GO_MK_BUILD_PKG
export GO_MK_INSTALL
# GO_MK_PLATFORMS is the optional "goos/goarch ..." matrix. When a consumer sets
# it (for example a daemon built for linux/amd64 and freebsd/amd64), the analysis
# gates and build-check run once per target with GOOS/GOARCH forced, so local and
# CI validate every platform from this one committed declaration. Empty leaves
# the host-only behavior unchanged.
export GO_MK_PLATFORMS

ifeq ($(filter go-build.mk,$(GO_MK_MODULES)),)
build: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" build-gate
	go build $(GO_BUILD_OUTPUT_FLAGS) $(GO_BUILD_FLAGS) $(GO_BUILD_TARGETS)

deploy:
	@if [ -z "$(strip $(GO_INSTALL_TARGET))" ]; then echo "deploy: GO_INSTALL_TARGET is not set"; exit 1; fi
	@printf 'deploy: installing %s\n' '$(GO_INSTALL_TARGET)'
	go install $(GO_INSTALL_FLAGS) $(GO_INSTALL_TARGET)

clean:
	@if [ -z "$(strip $(BINARY))" ]; then echo "clean: BINARY is not set (skipped)"; exit 0; fi
	rm -f $(BINARY)
endif

help:
	@printf '%s\n' 'Canonical entry points:'
	@printf '  %-40s %s\n' 'build' 'local build-check, then compile; CI skips inline gate only with OIDC proof'
	@printf '  %-40s %s\n' 'check' 'alias for lint'
	@printf '  %-40s %s\n' 'lint' 'run every lint gate'
	@printf '  %-40s %s\n' 'build-check' 'vet + lint + govulncheck'
	@printf '  %-40s %s\n' 'fmt' 'apply configured Go formatters'
	@printf '  %-40s %s\n' 'test' 'go test ./...'
	@printf '  %-40s %s\n' 'go-version-check' 'report whether go.mod tracks the latest Go release'
	@printf '\n%s\n' 'Scoped iteration:'
	@printf '  %-40s %s\n' 'lint-diff' 'run scoped lint against staged Go files'
	@printf '  %-40s %s\n' 'lint-files LINT_FILES=...' 'run scoped lint against listed files'
	@printf '  %-40s %s\n' 'lint-golangci-scope LINTER=.. RULE=..' 'run one golangci linter or rule against its baseline slice'
	@printf '\n%s\n' 'Lint sub-targets:'
	@printf '  %-40s %s\n' 'lint-tools' 'install golangci-lint, gofumpt, and goimports'
	@printf '  %-40s %s\n' 'lint-golangci' 'golangci-lint with baseline gate'
	@printf '  %-40s %s\n' 'lint-format' 'formatter diff gate'
	@printf '  %-40s %s\n' 'lint-gocyclo' 'gocyclo with baseline gate'
	@printf '  %-40s %s\n' 'lint-deadcode' 'deadcode with baseline gate'
	@printf '  %-40s %s\n' 'staticcheck-extra' 'custom analyzers with baseline gate'
	@printf '\n%s\n' 'Baseline maintenance (maintainer use, guarded by BASELINE_CONFIRM and BASELINE_TOKEN):'
	@printf '  %-40s %s\n' 'baseline' 'refresh the recorded baselines'
	@printf '  %-40s %s\n' 'lint-golangci-baseline-scope LINTER=.. RULE=..' 'baseline only one golangci linter or rule slice'
	@printf '\n%s\n' 'Pipeline maintenance:'
	@printf '  %-40s %s\n' 'go-mk-sync / update-go-mk' 'refresh go.mk, helper scripts, modules, and golangci.yml'
	@printf '  %-40s %s\n' 'smoke-fetch' 'force a fetch-path smoke run'

lint: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint

go-mk-notice: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" notice || true

lint-tools: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint-tools

lint-golangci: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint-golangci

lint-format: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint-format

lint-gocyclo: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint-gocyclo

lint-gocyclo-baseline: go-mk-bin
	@BASELINE_UPDATE_MODE=sync "$(GO_MK_BIN_RESOLVED)" baseline gocyclo

lint-gocyclo-baseline-prune-fixed: go-mk-bin
	@BASELINE_UPDATE_MODE=prune-fixed "$(GO_MK_BIN_RESOLVED)" baseline gocyclo

lint-gocyclo-baseline-remove-fixed: lint-gocyclo-baseline-prune-fixed

lint-gocyclo-baseline-accept-new: go-mk-bin
	@BASELINE_UPDATE_MODE=accept-new "$(GO_MK_BIN_RESOLVED)" baseline gocyclo

lint-files: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint-files

lint-diff: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint-diff

fmt: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" fmt

vet: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" vet

test: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" test

govulncheck: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" govulncheck

go-version-check: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" go-version-check

lint-deadcode: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint-deadcode

baseline-bin go-mk-bin:
	@bash "$(GO_MK_HELPER_DIR)/go-mk-bin.sh" bin

staticcheck-extra-bin: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" staticcheck-extra-bin

staticcheck-extra: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" staticcheck-extra

lint-golangci-baseline: go-mk-bin
	@BASELINE_UPDATE_MODE=sync "$(GO_MK_BIN_RESOLVED)" baseline golangci

lint-golangci-baseline-prune-fixed: go-mk-bin
	@BASELINE_UPDATE_MODE=prune-fixed "$(GO_MK_BIN_RESOLVED)" baseline golangci

lint-golangci-baseline-remove-fixed: lint-golangci-baseline-prune-fixed

lint-golangci-baseline-accept-new: go-mk-bin
	@BASELINE_UPDATE_MODE=accept-new "$(GO_MK_BIN_RESOLVED)" baseline golangci

lint-golangci-scope: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" lint-golangci-scope

lint-golangci-baseline-scope: go-mk-bin
	@BASELINE_UPDATE_MODE=sync "$(GO_MK_BIN_RESOLVED)" baseline golangci-scope

lint-golangci-baseline-scope-accept-new: go-mk-bin
	@BASELINE_UPDATE_MODE=accept-new "$(GO_MK_BIN_RESOLVED)" baseline golangci-scope

lint-deadcode-baseline: go-mk-bin
	@BASELINE_UPDATE_MODE=sync "$(GO_MK_BIN_RESOLVED)" baseline deadcode

lint-deadcode-baseline-prune-fixed: go-mk-bin
	@BASELINE_UPDATE_MODE=prune-fixed "$(GO_MK_BIN_RESOLVED)" baseline deadcode

lint-deadcode-baseline-remove-fixed: lint-deadcode-baseline-prune-fixed

lint-deadcode-baseline-accept-new: go-mk-bin
	@BASELINE_UPDATE_MODE=accept-new "$(GO_MK_BIN_RESOLVED)" baseline deadcode

staticcheck-extra-baseline: go-mk-bin
	@BASELINE_UPDATE_MODE=sync "$(GO_MK_BIN_RESOLVED)" baseline staticcheck-extra

staticcheck-extra-baseline-prune-fixed: go-mk-bin
	@BASELINE_UPDATE_MODE=prune-fixed "$(GO_MK_BIN_RESOLVED)" baseline staticcheck-extra

staticcheck-extra-baseline-remove-fixed: staticcheck-extra-baseline-prune-fixed

staticcheck-extra-baseline-accept-new: go-mk-bin
	@BASELINE_UPDATE_MODE=accept-new "$(GO_MK_BIN_RESOLVED)" baseline staticcheck-extra

build-check: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" build-check

build-gate: go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" build-gate

check: lint

baseline: go-mk-bin
	@BASELINE_UPDATE_MODE=sync "$(GO_MK_BIN_RESOLVED)" baseline all

baseline-prune-fixed: go-mk-bin
	@BASELINE_UPDATE_MODE=prune-fixed "$(GO_MK_BIN_RESOLVED)" baseline all

baseline-remove-fixed: baseline-prune-fixed

baseline-accept-new: go-mk-bin
	@BASELINE_UPDATE_MODE=accept-new "$(GO_MK_BIN_RESOLVED)" baseline all

baseline-add-new: baseline-accept-new

update-go-mk go-mk-sync:
	@bash "$(GO_MK_HELPER_DIR)/go-mk-sync.sh" update

smoke-fetch:
	@bash "$(GO_MK_HELPER_DIR)/go-mk-sync.sh" smoke-fetch

# GO_MK_GENERATE: opt-in codegen prerequisite. A consumer sets this BEFORE
# `include bootstrap.mk` to the name(s) of codegen target(s) that must run
# before any target loads or compiles packages (for example tree-sitter
# parser generation, proto, or go:embed payloads). Space-separated for
# multiple targets. The codegen target itself is defined in the consumer
# Makefile; go.mk only references it as an order-only prerequisite, so it
# runs first without forcing the .PHONY engine targets to rebuild.
GO_MK_GENERATE ?=

# GO_MK_WORKSPACE_USE: opt-in go.work routing. A consumer sets this BEFORE
# `include bootstrap.mk` to the use-paths of a workspace whose modules the
# proxy cannot build on their own (for example a submodule-vendored module
# whose C sources are absent from its module zip). go-mk-workspace then
# materializes a gitignored go.work from those paths before any compile, so a
# fresh checkout, worktree, or CI run routes the modules without a committed
# go.work or a go.mod replace (the latter is rejected by gomoddirectives). An
# existing go.work is left untouched, so a developer override survives.
GO_MK_WORKSPACE_USE ?=

.PHONY: go-mk-workspace
go-mk-workspace:
	@if [ -n "$(strip $(GO_MK_WORKSPACE_USE))" ] && [ ! -f go.work ]; then \
		echo "go-mk-workspace: creating go.work (use $(GO_MK_WORKSPACE_USE))"; \
		go work init $(GO_MK_WORKSPACE_USE); \
	fi

# go-mk-workspace runs after codegen so a use-path provided by a generated
# submodule has its go.mod before `go work init` reads it. go work init silently
# drops a use-path whose directory contains no module, which would otherwise
# leave the build to download that module from the proxy instead of using the
# local tree.
ifneq ($(strip $(GO_MK_GENERATE)),)
go-mk-workspace: | $(GO_MK_GENERATE)
endif

# Combined order-only prerequisites attached to every target that loads or
# compiles packages. Empty (the default) adds nothing. This block sits before
# the module include so the recipe-less build rule merges onto go-build.mk's
# build recipe. The CI matrix split legs lint-format and lint-gocyclo are
# deliberately omitted: gofumpt/goimports and gocyclo are textual or AST-only,
# they never compile or resolve packages, and they pass on a fresh runner
# without generated sources or a go.work, so attaching the prerequisite would
# only add cost.
GO_MK_PREREQS := $(if $(strip $(GO_MK_WORKSPACE_USE)),go-mk-workspace) $(GO_MK_GENERATE)
ifneq ($(strip $(GO_MK_PREREQS)),)
build build-check check lint lint-golangci lint-deadcode staticcheck-extra vet test govulncheck: | $(GO_MK_PREREQS)
endif

# Include opt-in modules at end so they see all go.mk definitions.
$(foreach m,$(GO_MK_MODULES),$(eval -include .make/$(m)))
