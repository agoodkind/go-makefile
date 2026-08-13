# go-build.mk: universal build/install/uninstall pipeline.
#
# Project Makefile must set:
#   BINARY := <name>            # e.g., lm-review
#   CMD    := ./cmd/$(BINARY)   # main package
#   VPKG   := <import path>     # e.g., goodkind.io/lm-review/internal/version
#                               #   stamped fields: Commit, Version, Dirty, BuildTime
#
# Optional:
#   GKLOG_VPKG  := goodkind.io/gklog/version    # cross-stamp gklog
#   LIBRARY     := 1                            # opt-out: build/install no-op
#   INSTALL_DIR := <dir>                        # default $XDG_BIN_HOME or ~/.local/bin
#   GO_BUILD_TAGS := tag1,tag2                  # comma-separated build tags
#   CGO_ENABLED                                 # exported by project if needed
#
# Targets exposed: build, deploy, install, uninstall, version-info, clean-dist.
# Override go.mk's `build`/`deploy`/`clean` defaults with the standardized flow.
#
# Strict staticcheck-extra is the default for every consumer that opts into
# this module. Projects can soften via STATICCHECK_EXTRA_FLAGS in their own
# Makefile if needed.
STATICCHECK_EXTRA_FLAGS ?= $(STATICCHECK_EXTRA_CORE_FLAGS) $(STATICCHECK_EXTRA_STRICT_FLAGS)

.PHONY: build deploy install uninstall version-info clean-dist

# Auto-detect mode. LIBRARY mode skips build/install (lint/test/vet still apply
# from go.mk). The default is binary mode, requiring BINARY+CMD+VPKG.
LIBRARY ?=

ifeq ($(strip $(LIBRARY)),1)

# Library mode: no binary to produce, but the same build gate still runs.
build: | go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" build-gate
	@echo "library mode: no binary to build"

deploy: install

install:
	@echo "library mode: install is a no-op"

uninstall:
	@echo "library mode: uninstall is a no-op"

version-info:
	@echo "library mode: no version stamping"

clean-dist:
	@:

# Targets that actually compile, for the GO_MK_PREREQS attachment below.
GO_MK_COMPILE_TARGETS := build install

else

# ---------------------------------------------------------------------------
# Binary mode
# ---------------------------------------------------------------------------

ifeq ($(strip $(BINARY)),)
$(error go-build.mk: BINARY is not set)
endif
ifeq ($(strip $(CMD)),)
$(error go-build.mk: CMD is not set)
endif

DIST_DIR ?= dist
DIST_BIN := $(DIST_DIR)/$(BINARY)

INSTALL_DIR ?= $(or $(XDG_BIN_HOME),$(HOME)/.local/bin)
INSTALL_BIN := $(INSTALL_DIR)/$(BINARY)

# Extra binaries beyond the primary BINARY, declared as space-separated
# name:cmd pairs (an optional third field name:cmd:dir overrides INSTALL_DIR for
# that binary). Empty means the single BINARY:CMD, so single-binary repos
# declare nothing. The go-mk install/build/uninstall commands read this.
INSTALL_BINS ?=

# The full set of binaries a release ships, declared as space-separated
# name:cmd pairs. Empty defaults to the single BINARY:CMD. When set it replaces
# that default and MUST include the primary BINARY (the release fails loudly
# otherwise); the primary binary's name titles the GitHub release.
RELEASE_BINS ?=

# ---------------------------------------------------------------------------
# Freshness inputs
# ---------------------------------------------------------------------------
# build and install hang off the real files they produce, so make skips the
# engine call outright when nothing is newer: no gate, no compile, no codesign,
# no copy. The lists below are what make compares those files against.

# Every path the engine writes. An empty INSTALL_BINS means the single
# BINARY:CMD. A non-empty one replaces that default outright and need not name
# BINARY, so both lists are derived from it rather than assumed to contain
# $(DIST_BIN) or $(INSTALL_BIN).
go-mk-bin-field = $(word $(2),$(subst :, ,$(1)))
go-mk-install-path = $(or $(call go-mk-bin-field,$(1),3),$(INSTALL_DIR))/$(call go-mk-bin-field,$(1),1)

ifeq ($(strip $(INSTALL_BINS)),)
GO_MK_DIST_PATHS    := $(DIST_BIN)
GO_MK_INSTALL_PATHS := $(INSTALL_BIN)
else
GO_MK_DIST_PATHS    := $(foreach spec,$(INSTALL_BINS),$(DIST_DIR)/$(call go-mk-bin-field,$(spec),1))
GO_MK_INSTALL_PATHS := $(foreach spec,$(INSTALL_BINS),$(call go-mk-install-path,$(spec)))
endif

# A single engine call writes every path, and GNU Make 3.81 has no grouped
# targets, so the first path carries the rule and the rest re-check cheaply.
GO_MK_DIST_PRIMARY      := $(firstword $(GO_MK_DIST_PATHS))
GO_MK_DIST_SECONDARY    := $(filter-out $(GO_MK_DIST_PRIMARY),$(GO_MK_DIST_PATHS))
GO_MK_INSTALL_PRIMARY   := $(firstword $(GO_MK_INSTALL_PATHS))
GO_MK_INSTALL_SECONDARY := $(filter-out $(GO_MK_INSTALL_PRIMARY),$(GO_MK_INSTALL_PATHS))

# The packages build and install read. Consumers declare these: CMD names the
# main package, INSTALL_BINS names one main package per binary, and each gate
# carries its own target pattern. The union is what a run of build or install
# actually compiles and lints, so it is the union that decides staleness.
# GOCYCLO_TARGETS is absent because it names files rather than packages.
GO_MK_BUILD_PACKAGES := $(sort \
	$(foreach spec,$(INSTALL_BINS),$(call go-mk-bin-field,$(spec),2)) \
	$(GO_BUILD_TARGETS) \
	$(GO_VET_TARGETS) \
	$(GOLANGCI_LINT_TARGETS) \
	$(DEADCODE_TARGETS) \
	$(STATICCHECK_EXTRA_TARGETS) \
	$(GOVULNCHECK_TARGETS))

# Go reports the exact files each package compiles, so the file list comes from
# go list rather than from a pattern match over the tree. Test files are in the
# set because the lint gates read them. The template keeps only packages in the
# main module, which drops the standard library and the module cache without
# comparing path prefixes: go list reports a directory as it was reached, so a
# prefix comparison disagrees with itself across symlinked checkouts.
#
# go list runs at parse time on every invocation, including goals that never
# build, and takes about half a second on a warm cache. Deferring it would
# require .SECONDEXPANSION:, which changes $$ handling for every rule in every
# consumer, so the flat cost is preferred.
GO_MK_BUILD_SOURCE_TEMPLATE := {{if and .Module .Module.Main}}{{$$d := .Dir}}\
{{range .GoFiles}}{{$$d}}/{{.}} {{end}}\
{{range .CgoFiles}}{{$$d}}/{{.}} {{end}}\
{{range .CFiles}}{{$$d}}/{{.}} {{end}}\
{{range .CXXFiles}}{{$$d}}/{{.}} {{end}}\
{{range .HFiles}}{{$$d}}/{{.}} {{end}}\
{{range .SFiles}}{{$$d}}/{{.}} {{end}}\
{{range .EmbedFiles}}{{$$d}}/{{.}} {{end}}\
{{range .TestGoFiles}}{{$$d}}/{{.}} {{end}}\
{{range .XTestGoFiles}}{{$$d}}/{{.}} {{end}}{{end}}

GO_MK_BUILD_SOURCES := $(wildcard go.mod go.sum $(GO_MK_GOLANGCI_CONFIG)) \
	$(GO_MK_GENERATE_OUTPUTS) \
	$(shell go list -e -deps -f '$(GO_MK_BUILD_SOURCE_TEMPLATE)' \
		$(GO_MK_BUILD_PACKAGES) 2>/dev/null)

# Declared generated outputs are prerequisites even when absent, so a deleted
# one runs codegen and then the compile in the same invocation. go list cannot
# report a file that does not exist yet, which is why these are named rather
# than discovered.
ifneq ($(strip $(GO_MK_GENERATE_OUTPUTS)),)
$(GO_MK_GENERATE_OUTPUTS): | $(GO_MK_GENERATE)
	@test -e $@ || { printf 'go-build.mk: codegen did not produce %s\n' '$@' >&2; exit 1; }
endif

# Codegen inputs are not Go packages, so go list cannot see them. A consumer
# that declares them gets them compared directly. Directories are in the list
# alongside their files: adding or removing a file changes only the timestamp
# of the directory holding it, and a file list alone would miss that.
ifneq ($(strip $(GO_MK_GENERATE_INPUTS)),)
GO_MK_BUILD_SOURCES += $(GO_MK_GENERATE_INPUTS) \
	$(shell find $(GO_MK_GENERATE_INPUTS) -name .git -prune -o -print 2>/dev/null)
endif

# go list returns nothing when the module cannot load, which happens before
# codegen has produced its sources. An empty list would make every output look
# current, so the rules fall back to running unconditionally instead.
ifeq ($(strip $(GO_MK_BUILD_SOURCES)),)
.PHONY: go-mk-build-sources-unknown
go-mk-build-sources-unknown:
	@:
GO_MK_BUILD_SOURCES := go-mk-build-sources-unknown
endif

# Version metadata derived from git. Single canonical scheme across all repos.
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_DIRTY   := $(shell git diff --quiet 2>/dev/null && echo false || echo true)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Stamped LDFLAGS. VPKG is optional; when set, the project's version package
# must define matching exported string vars (Commit, Version, Dirty, BuildTime).
# When unset, no canonical stamping happens. If GKLOG_VPKG is set, cross-stamp
# gklog as well.
#
# Projects with non-standard version field naming (e.g. unexported, or different
# names) can pre-populate GO_BUILD_LDFLAGS in their Makefile BEFORE -include
# $(GO_MK); the ?= here preserves their value, and the conditional += blocks
# below still extend it for VPKG/GKLOG_VPKG when set.
GO_BUILD_LDFLAGS ?=

# The consumer's own ldflags, captured before the git stamps below extend them.
# The build-configuration fingerprint uses this rather than the final value,
# because the stamps carry a commit and a timestamp that move on their own.
GO_MK_LDFLAGS_BASE := $(GO_BUILD_LDFLAGS)

ifneq ($(strip $(VPKG)),)
GO_BUILD_LDFLAGS += \
	-X $(VPKG).Commit=$(GIT_COMMIT) \
	-X $(VPKG).Version=$(GIT_VERSION) \
	-X $(VPKG).Dirty=$(GIT_DIRTY) \
	-X $(VPKG).BuildTime=$(BUILD_TIME)
endif

ifneq ($(strip $(GKLOG_VPKG)),)
GO_BUILD_LDFLAGS += \
	-X $(GKLOG_VPKG).Version=$(GIT_VERSION) \
	-X $(GKLOG_VPKG).Commit=$(GIT_COMMIT) \
	-X $(GKLOG_VPKG).Dirty=$(GIT_DIRTY) \
	-X $(GKLOG_VPKG).BuildTime=$(BUILD_TIME) \
	-X $(GKLOG_VPKG).BinHash=
endif

GO_BUILD_TAGS          ?=
GO_BUILD_TAGS_FLAG     := $(if $(strip $(GO_BUILD_TAGS)),-tags '$(GO_BUILD_TAGS)',)
GO_BUILD_LDFLAGS_FLAG  := $(if $(strip $(GO_BUILD_LDFLAGS)),-ldflags '$(GO_BUILD_LDFLAGS)',)
GO_BUILD_EXTRA_FLAGS   ?=

# Override go.mk's GO_BUILD_FLAGS so its `build` target picks up our ldflags
# even when called via the legacy path. The standardized `build` below uses
# the same vars.
GO_BUILD_FLAGS := $(GO_BUILD_TAGS_FLAG) $(GO_BUILD_LDFLAGS_FLAG) $(GO_BUILD_EXTRA_FLAGS)

# Codesign: macOS-only, opt-in. Project sets BUNDLE_ID; identity is
# auto-detected from the keychain or pinned via CERT_ID in config.mk.
# CODESIGN_TIMESTAMP defaults to `none` for local dev (no Apple timestamp
# server round-trip); release flow overrides to `timestamp` for notarize.
# Linux/Windows skip the macro entirely via the uname guard.
BUNDLE_ID          ?= io.goodkind.$(BINARY)
CODESIGN_IDENTITY  ?= $(or $(CERT_ID),$(shell if [ "$$(uname)" = "Darwin" ]; then security find-identity -v -p codesigning 2>/dev/null | awk '/Developer ID Application/ { print $$2; exit }'; fi))
CODESIGN_TIMESTAMP ?= none
CODESIGN_ENTITLEMENTS ?=
GO_MK_INSTALL_PRE_CMD ?=
GO_MK_INSTALL_POST_CMD ?=
# Inputs the go-mk install/build/uninstall commands read from the environment.
# go-mk assembles the build argv from the GO_BUILD_* values, stamps nothing
# itself (the ldflags are computed above), signs on macOS from the CODESIGN_*
# values, and installs each declared binary.
export BINARY
export CMD
export VPKG
export GKLOG_VPKG
export DIST_DIR
export INSTALL_DIR
export INSTALL_BINS
export RELEASE_BINS
export GO_BUILD_TAGS
export GO_BUILD_LDFLAGS
export GO_BUILD_EXTRA_FLAGS
export BUNDLE_ID
export CODESIGN_IDENTITY
export CODESIGN_TIMESTAMP
export CODESIGN_ENTITLEMENTS
export GO_MK_INSTALL_PRE_CMD
export GO_MK_INSTALL_POST_CMD

# Build settings change the artifact without touching a source file, so they
# are compared through a stamp that is rewritten only when the settings differ.
# The git stamps and BUILD_TIME stay out: they move on their own and would
# rebuild every run. The consumer's own ldflags are in, through the value
# captured before the git stamps extended it. GOOS and GOARCH are in because
# they select which files a package compiles, and go list reports only the
# files the current context selects.
GO_MK_BUILD_CONFIG_DIR := .make/build-config
GO_MK_BUILD_CONFIG := \
	binary=$(BINARY) cmd=$(CMD) bins=$(INSTALL_BINS) \
	dist=$(DIST_DIR) install_dir=$(INSTALL_DIR) \
	tags=$(GO_BUILD_TAGS) extra=$(GO_BUILD_EXTRA_FLAGS) ldflags=$(GO_MK_LDFLAGS_BASE) \
	cgo=$(CGO_ENABLED) goflags=$(GOFLAGS) goos=$(GOOS) goarch=$(GOARCH) \
	vpkg=$(VPKG) gklog_vpkg=$(GKLOG_VPKG) \
	bundle=$(BUNDLE_ID) identity=$(CODESIGN_IDENTITY) timestamp=$(CODESIGN_TIMESTAMP) \
	entitlements=$(CODESIGN_ENTITLEMENTS) \
	pre=$(GO_MK_INSTALL_PRE_CMD) post=$(GO_MK_INSTALL_POST_CMD)

# Quotes, backslashes, and dollars would not survive the shell round trip, and
# the stamp only has to differ when the settings differ, so they are dropped.
GO_MK_SQUOTE := '
GO_MK_DQUOTE := "
GO_MK_BUILD_CONFIG_SAFE := $(subst $$,,$(subst \,,$(subst $(GO_MK_DQUOTE),,$(subst $(GO_MK_SQUOTE),,$(GO_MK_BUILD_CONFIG)))))

# The settings are carried in the stamp's name rather than its contents, so a
# changed setting names a file that does not exist and make sees an ordinary
# missing prerequisite. A stamp with fixed contents would need a phony trigger
# to be re-evaluated, and that would make every target look perpetually out of
# date to `make -n` and `make -q`.
GO_MK_BUILD_CONFIG_ID := $(shell printf '%s' '$(GO_MK_BUILD_CONFIG_SAFE)' | cksum | tr -cd '0-9')
GO_MK_BUILD_CONFIG_STAMP := $(GO_MK_BUILD_CONFIG_DIR)/$(GO_MK_BUILD_CONFIG_ID)

$(GO_MK_BUILD_CONFIG_STAMP):
	@mkdir -p $(GO_MK_BUILD_CONFIG_DIR)
	@rm -f $(GO_MK_BUILD_CONFIG_DIR)/*
	@printf '%s\n' '$(GO_MK_BUILD_CONFIG_SAFE)' > $@

# A name for the stamp, so nothing outside this file has to know the hash.
.PHONY: go-mk-build-config
go-mk-build-config: $(GO_MK_BUILD_CONFIG_STAMP)

# build and install run the go-mk build gate before compiling. Local builds run
# vet, lint, and govulncheck inline; GitHub Actions skips that inline gate only
# after OIDC proof because the reusable CI workflow has a separate gate job.
# install builds every declared binary before placing it. Signing runs inside
# the engine on macOS only.
#
# build and install are name wrappers; the work hangs off the real files they
# produce, so an unchanged tree runs no recipe at all. Both file rules depend on
# sources rather than on each other: chaining $(INSTALL_BIN) to $(DIST_BIN)
# would pay the gate twice on every real change, because the engine's install
# command runs the gate and the compile itself. Running `make build install`
# together still gates twice; either one alone gates once.
build: $(GO_MK_DIST_PATHS)

$(GO_MK_DIST_PRIMARY): $(GO_MK_BUILD_SOURCES) $(GO_MK_BUILD_CONFIG_STAMP) | go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" build

deploy: install

install: $(GO_MK_INSTALL_PATHS)

$(GO_MK_INSTALL_PRIMARY): $(GO_MK_BUILD_SOURCES) $(GO_MK_BUILD_CONFIG_STAMP) | go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" install

# An install hook is a side effect the consumer expects on every install, not
# a step whose result is the installed file. Declaring one opts that repo out
# of skipping, so a hook that restarts a service or publishes an artifact still
# runs when the binary is already current.
ifneq ($(strip $(GO_MK_INSTALL_PRE_CMD)$(GO_MK_INSTALL_POST_CMD)),)
.PHONY: go-mk-install-hooks-declared
go-mk-install-hooks-declared:
	@:

$(GO_MK_INSTALL_PRIMARY): go-mk-install-hooks-declared
endif

# The primary rule writes every declared binary, so a secondary one is normally
# already in place by the time make reaches it. The guard covers the case where
# the primary was current and a secondary was deleted, and it runs the engine
# at most once because the first call restores all of them.
$(GO_MK_DIST_SECONDARY): $(GO_MK_DIST_PRIMARY) | go-mk-bin
	@test -x $@ || "$(GO_MK_BIN_RESOLVED)" build

$(GO_MK_INSTALL_SECONDARY): $(GO_MK_INSTALL_PRIMARY) | go-mk-bin
	@test -x $@ || "$(GO_MK_BIN_RESOLVED)" install

uninstall: | go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" uninstall

version-info:
	@echo "binary:      $(BINARY)"
	@echo "cmd:         $(CMD)"
	@echo "vpkg:        $(VPKG)"
	@echo "gklog_vpkg:  $(GKLOG_VPKG)"
	@echo "commit:      $(GIT_COMMIT)"
	@echo "version:     $(GIT_VERSION)"
	@echo "dirty:       $(GIT_DIRTY)"
	@echo "build_time:  $(BUILD_TIME)"
	@echo "tags:        $(GO_BUILD_TAGS)"
	@echo "cgo_enabled: $(CGO_ENABLED)"
	@echo "codesign_entitlements: $(CODESIGN_ENTITLEMENTS)"
	@echo "install_dir: $(INSTALL_DIR)"

clean-dist:
	@rm -rf $(DIST_DIR)
	@echo "cleaned: $(DIST_DIR)"

# Targets that actually compile, for the GO_MK_PREREQS attachment below. These
# are the file rules, not the build/install wrappers: make orders a target's
# recipe after its prerequisites, but leaves the order among those
# prerequisites unspecified, so codegen hung off the wrapper could run after
# the compile it is supposed to precede.
GO_MK_COMPILE_TARGETS := $(GO_MK_DIST_PATHS) $(GO_MK_INSTALL_PATHS)

endif

# GO_MK_PREREQS (see go.mk): codegen and go.work routing. Attach to this
# module's compile targets so a consumer that opts into go-build.mk also
# generates its parsers/proto and materializes go.work before build and install.
# Empty default is a no-op.
ifneq ($(strip $(GO_MK_PREREQS)),)
$(GO_MK_COMPILE_TARGETS): | $(GO_MK_PREREQS)
endif
