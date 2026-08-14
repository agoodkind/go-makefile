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
	@"$(__GO_MK_BIN_RESOLVED)" build-gate
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

# Targets that actually compile, for the __GO_MK_PREREQS attachment below.
__GO_MK_COMPILE_TARGETS := build install

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
__GO_MK_DIST_BIN := $(DIST_DIR)/$(BINARY)

INSTALL_DIR ?= $(or $(XDG_BIN_HOME),$(HOME)/.local/bin)
__GO_MK_INSTALL_BIN := $(INSTALL_DIR)/$(BINARY)

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
# $(__GO_MK_DIST_BIN) or $(__GO_MK_INSTALL_BIN).
__go-mk-bin-field = $(word $(2),$(subst :, ,$(1)))
__go-mk-installed-path = $(or $(call __go-mk-bin-field,$(1),3),$(INSTALL_DIR))/$(call __go-mk-bin-field,$(1),1)

ifeq ($(strip $(INSTALL_BINS)),)
__GO_MK_DIST_OUTPUTS    := $(__GO_MK_DIST_BIN)
__GO_MK_INSTALL_OUTPUTS := $(__GO_MK_INSTALL_BIN)
else
__GO_MK_DIST_OUTPUTS    := $(foreach spec,$(INSTALL_BINS),$(DIST_DIR)/$(call __go-mk-bin-field,$(spec),1))
__GO_MK_INSTALL_OUTPUTS := $(foreach spec,$(INSTALL_BINS),$(call __go-mk-installed-path,$(spec)))
endif

# A single engine call writes every path, and GNU Make 3.81 has no grouped
# targets, so the first path carries the rule and the rest re-check cheaply.
__GO_MK_DIST_FIRST_OUTPUT     := $(firstword $(__GO_MK_DIST_OUTPUTS))
__GO_MK_DIST_OTHER_OUTPUTS    := $(filter-out $(__GO_MK_DIST_FIRST_OUTPUT),$(__GO_MK_DIST_OUTPUTS))
__GO_MK_INSTALL_FIRST_OUTPUT  := $(firstword $(__GO_MK_INSTALL_OUTPUTS))
__GO_MK_INSTALL_OTHER_OUTPUTS := $(filter-out $(__GO_MK_INSTALL_FIRST_OUTPUT),$(__GO_MK_INSTALL_OUTPUTS))

# The packages build and install read. Consumers declare these: CMD names the
# main package, INSTALL_BINS names one main package per binary, and each gate
# carries its own target pattern. The union is what a run of build or install
# actually compiles and lints, so it is the union that decides staleness.
# GOCYCLO_TARGETS is absent because it names files rather than packages. CMD is
# named directly for the single-binary case, so a repo that narrows the gate
# patterns still reports the package its binary is built from.
__GO_MK_GATED_PACKAGES := $(sort \
	$(if $(strip $(INSTALL_BINS)),$(foreach spec,$(INSTALL_BINS),$(call __go-mk-bin-field,$(spec),2)),$(CMD)) \
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
#
# The template emits one path per line and is one physical line, because a
# continuation would join the groups with a space and that space would land in
# the output.
#
# go list -e reports a package it could not load instead of failing, which
# happens while a generated package is still missing. The template marks such a
# package so a partial list is not mistaken for a complete one.
__GO_MK_LOAD_ERROR_MARK := go-mk-load-error
__GO_MK_PACKAGE_FILE_TEMPLATE := {{if .Error}}$(__GO_MK_LOAD_ERROR_MARK){{"\n"}}{{end}}{{if and .Module .Module.Main}}{{$$d := .Dir}}{{range .GoFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .CgoFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .CFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .CXXFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .HFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .SFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .SysoFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .EmbedFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$$d}}/{{.}}{{"\n"}}{{end}}{{end}}

# A prerequisite list cannot carry a path holding a space, #, $, %, :, ;, =, or
# a backslash: make would split it or read it as syntax. One such path discards
# the whole discovered list, and the guard below then runs the engine every
# time. Losing the skip is the safe direction; a silently short list is not.
__GO_MK_DROP_UNUSABLE_PATHS := awk '{ if ($$0 ~ /[ \#$$%:;=\\]/) unsafe=1; line[NR]=$$0 } \
	END { if (!unsafe) for (i = 1; i <= NR; i++) print line[i] }'

# A declared path reaches a shell before that filter can see it, so each one is
# single quoted with its own single quotes escaped. Without the escape a quote
# in the value ends the quoting and the rest runs as a command.
__GO_MK_SQUOTE := '
__GO_MK_SQUOTE_ESCAPED := '\''
__go-mk-shell-quote = $(__GO_MK_SQUOTE)$(subst $(__GO_MK_SQUOTE),$(__GO_MK_SQUOTE_ESCAPED),$(1))$(__GO_MK_SQUOTE)

__GO_MK_PACKAGE_FILES := $(shell go list -e -deps -f '$(__GO_MK_PACKAGE_FILE_TEMPLATE)' \
	$(__GO_MK_GATED_PACKAGES) 2>/dev/null | $(__GO_MK_DROP_UNUSABLE_PATHS))

# Set when the input list cannot be trusted, which makes every output run
# instead of comparing timestamps against a list that is short or wrong. Losing
# a skip costs time; a wrong skip ships a stale binary. A missing file is
# dropped from the list rather than named, because make stops with "No rule to
# make target" on a prerequisite it cannot build, and this flag covers the gap.
__GO_MK_INPUTS_UNKNOWN :=

ifneq ($(filter $(__GO_MK_LOAD_ERROR_MARK),$(__GO_MK_PACKAGE_FILES)),)
__GO_MK_INPUTS_UNKNOWN := 1
__GO_MK_PACKAGE_FILES := $(filter-out $(__GO_MK_LOAD_ERROR_MARK),$(__GO_MK_PACKAGE_FILES))
endif

# The lint config is compared by content rather than by timestamp, through the
# settings stamp below. go.mk rewrites it on every run, so its timestamp is
# always the current one and would mark every output stale.
__GO_MK_LINT_CONFIG_DIGEST := $(shell cksum < $(call __go-mk-shell-quote,$(GO_MK_GOLANGCI_CONFIG)) \
	2>/dev/null | tr -cd '0-9 ' | tr ' ' '-')

__GO_MK_FRESHNESS_INPUTS := $(wildcard go.mod go.sum) \
	$(__GO_MK_PACKAGE_FILES)

# Declared generated outputs are prerequisites even when absent, so a deleted
# one runs codegen and then the compile in the same invocation. go list cannot
# report a file that does not exist yet, which is why these are named rather
# than discovered. They go through the same path filter as everything else,
# because a declared path is no safer than a discovered one.
ifneq ($(strip $(GO_MK_GENERATE_OUTPUTS)),)
__GO_MK_QUOTED_GENERATED_PATHS := $(foreach path,$(GO_MK_GENERATE_OUTPUTS),$(call __go-mk-shell-quote,$(path)))
__GO_MK_GENERATED_PATHS := $(shell printf '%s\n' $(__GO_MK_QUOTED_GENERATED_PATHS) \
	| $(__GO_MK_DROP_UNUSABLE_PATHS))
__GO_MK_FRESHNESS_INPUTS += $(__GO_MK_GENERATED_PATHS)

ifeq ($(strip $(__GO_MK_GENERATED_PATHS)),)
__GO_MK_INPUTS_UNKNOWN := 1
else
$(__GO_MK_GENERATED_PATHS): | $(GO_MK_GENERATE)
	@test -e $@ || { printf 'go-build.mk: codegen did not produce %s\n' '$@' >&2; exit 1; }
endif
endif

# Codegen inputs are not Go packages, so go list cannot see them. A consumer
# that declares them gets them compared directly. find prints each declared
# directory before its contents, so adding or removing a file is seen through
# the containing directory's timestamp. The same path filter applies, because a
# declared path is no safer than a discovered one, and each path is quoted
# before the shell sees it so a metacharacter cannot run as a command.
ifneq ($(strip $(GO_MK_GENERATE_INPUTS)),)
__GO_MK_QUOTED_CODEGEN_INPUTS := $(foreach path,$(GO_MK_GENERATE_INPUTS),$(call __go-mk-shell-quote,$(path)))
__GO_MK_CODEGEN_INPUT_PATHS := $(shell find $(__GO_MK_QUOTED_CODEGEN_INPUTS) -name .git -prune -o \
	-print 2>/dev/null | $(__GO_MK_DROP_UNUSABLE_PATHS))
__GO_MK_FRESHNESS_INPUTS += $(__GO_MK_CODEGEN_INPUT_PATHS)
ifeq ($(strip $(__GO_MK_CODEGEN_INPUT_PATHS)),)
__GO_MK_INPUTS_UNKNOWN := 1
endif
endif

# The discovered list is empty when the module cannot load, which happens
# before codegen has produced its sources, and when a path make cannot carry
# discarded it. Binary mode always has at least CMD to report, so an empty list
# is a signal rather than a valid answer.
ifeq ($(strip $(__GO_MK_PACKAGE_FILES)),)
__GO_MK_INPUTS_UNKNOWN := 1
endif

# Version metadata derived from git. Single canonical scheme across all repos.
__GO_MK_GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
__GO_MK_GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
__GO_MK_GIT_DIRTY   := $(shell git diff --quiet 2>/dev/null && echo false || echo true)
__GO_MK_BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

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
__GO_MK_CONSUMER_LDFLAGS := $(GO_BUILD_LDFLAGS)

ifneq ($(strip $(VPKG)),)
GO_BUILD_LDFLAGS += \
	-X $(VPKG).Commit=$(__GO_MK_GIT_COMMIT) \
	-X $(VPKG).Version=$(__GO_MK_GIT_VERSION) \
	-X $(VPKG).Dirty=$(__GO_MK_GIT_DIRTY) \
	-X $(VPKG).BuildTime=$(__GO_MK_BUILD_TIME)
endif

ifneq ($(strip $(GKLOG_VPKG)),)
GO_BUILD_LDFLAGS += \
	-X $(GKLOG_VPKG).Version=$(__GO_MK_GIT_VERSION) \
	-X $(GKLOG_VPKG).Commit=$(__GO_MK_GIT_COMMIT) \
	-X $(GKLOG_VPKG).Dirty=$(__GO_MK_GIT_DIRTY) \
	-X $(GKLOG_VPKG).BuildTime=$(__GO_MK_BUILD_TIME) \
	-X $(GKLOG_VPKG).BinHash=
endif

GO_BUILD_TAGS          ?=
__GO_MK_TAGS_FLAG     := $(if $(strip $(GO_BUILD_TAGS)),-tags '$(GO_BUILD_TAGS)',)
__GO_MK_LDFLAGS_FLAG  := $(if $(strip $(GO_BUILD_LDFLAGS)),-ldflags '$(GO_BUILD_LDFLAGS)',)
GO_BUILD_EXTRA_FLAGS   ?=

# Override go.mk's GO_BUILD_FLAGS so its `build` target picks up our ldflags
# even when called via the legacy path. The standardized `build` below uses
# the same vars.
GO_BUILD_FLAGS := $(__GO_MK_TAGS_FLAG) $(__GO_MK_LDFLAGS_FLAG) $(GO_BUILD_EXTRA_FLAGS)

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

# The entitlements file is read by codesign, so its contents decide what the
# signed binary is allowed to do. Its path is in the settings stamp; its
# contents belong in the source list.
ifneq ($(strip $(CODESIGN_ENTITLEMENTS)),)
__GO_MK_ENTITLEMENTS_PATH := $(shell printf '%s\n' $(call __go-mk-shell-quote,$(CODESIGN_ENTITLEMENTS)) \
	| $(__GO_MK_DROP_UNUSABLE_PATHS))
__GO_MK_FRESHNESS_INPUTS += $(wildcard $(__GO_MK_ENTITLEMENTS_PATH))
ifeq ($(strip $(wildcard $(__GO_MK_ENTITLEMENTS_PATH))),)
__GO_MK_INPUTS_UNKNOWN := 1
endif
endif

# One always-run prerequisite for every case that could not describe the inputs
# honestly. It is added here, after the last contribution to the source list.
ifneq ($(strip $(__GO_MK_INPUTS_UNKNOWN)),)
.PHONY: __go-mk-inputs-unknown
__go-mk-inputs-unknown:
	@:
__GO_MK_FRESHNESS_INPUTS += __go-mk-inputs-unknown
endif

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
# The consumer's own ldflags are in, through the value captured before the git
# stamps extended it. GOOS and GOARCH are in because they select which files a
# package compiles, and go list reports only the files the current context
# selects. The commit, version, and dirty flag are in so the identity the
# binary reports matches the tree it was built from, which costs one rebuild
# after a commit, a tag, or a move between a clean and a dirty tree. __GO_MK_BUILD_TIME
# is the one stamped value left out, because it moves every second and would
# rebuild on every invocation.
#
# The source list itself is in the stamp, because a timestamp comparison sees
# only files that still exist. Deleting a source, or adding one, changes the
# list and so changes the stamp name.
__GO_MK_SETTINGS_DIR := .make/build-config
__GO_MK_SETTINGS := \
	binary=$(BINARY) cmd=$(CMD) bins=$(INSTALL_BINS) \
	dist=$(DIST_DIR) install_dir=$(INSTALL_DIR) \
	tags=$(GO_BUILD_TAGS) extra=$(GO_BUILD_EXTRA_FLAGS) ldflags=$(__GO_MK_CONSUMER_LDFLAGS) \
	cgo=$(CGO_ENABLED) goflags=$(GOFLAGS) goos=$(GOOS) goarch=$(GOARCH) \
	vpkg=$(VPKG) gklog_vpkg=$(GKLOG_VPKG) \
	commit=$(__GO_MK_GIT_COMMIT) version=$(__GO_MK_GIT_VERSION) dirty=$(__GO_MK_GIT_DIRTY) \
	files=$(sort $(__GO_MK_FRESHNESS_INPUTS)) lint=$(__GO_MK_LINT_CONFIG_DIGEST) \
	bundle=$(BUNDLE_ID) identity=$(CODESIGN_IDENTITY) timestamp=$(CODESIGN_TIMESTAMP) \
	entitlements=$(CODESIGN_ENTITLEMENTS) \
	pre=$(GO_MK_INSTALL_PRE_CMD) post=$(GO_MK_INSTALL_POST_CMD)

# Quotes, backslashes, and dollars would not survive the shell round trip, so
# each becomes a spelled-out token instead of disappearing. Deleting them would
# let two different settings spell one name. The hyphen is escaped first, which
# is what keeps the mapping reversible and so free of collisions.
__GO_MK_DQUOTE := "
__GO_MK_SETTINGS_SAFE := $(subst $$,-dl-,$(subst \,-bs-,$(subst $(__GO_MK_DQUOTE),-dq-,$(subst $(__GO_MK_SQUOTE),-sq-,$(subst -,-h-,$(__GO_MK_SETTINGS))))))

# The settings are carried in the stamp's name rather than its contents, so a
# changed setting names a file that does not exist and make sees an ordinary
# missing prerequisite. A stamp with fixed contents would need a phony trigger
# to be re-evaluated, and that would make every target look perpetually out of
# date to `make -n` and `make -q`.
# cksum reports a checksum and a byte count. Both are in the name, separated,
# because concatenating the digits would let one pair of values spell the same
# name as another.
__GO_MK_SETTINGS_ID := $(shell printf '%s' '$(__GO_MK_SETTINGS_SAFE)' | cksum | awk '{printf "%s-%s", $$1, $$2}')
__GO_MK_SETTINGS_STAMP := $(__GO_MK_SETTINGS_DIR)/$(__GO_MK_SETTINGS_ID)

$(__GO_MK_SETTINGS_STAMP):
	@mkdir -p $(__GO_MK_SETTINGS_DIR)
	@rm -f $(__GO_MK_SETTINGS_DIR)/*
	@printf '%s\n' '$(__GO_MK_SETTINGS_SAFE)' > $@

# A name for the stamp, so nothing outside this file has to know the hash.
.PHONY: __go-mk-settings-stamp
__go-mk-settings-stamp: $(__GO_MK_SETTINGS_STAMP)

# build and install run the go-mk build gate before compiling. Local builds run
# vet, lint, and govulncheck inline; GitHub Actions skips that inline gate only
# after OIDC proof because the reusable CI workflow has a separate gate job.
# install builds every declared binary before placing it. Signing runs inside
# the engine on macOS only.
#
# build and install are name wrappers; the work hangs off the real files they
# produce, so an unchanged tree runs no recipe at all. Both file rules depend on
# sources rather than on each other: chaining $(__GO_MK_INSTALL_BIN) to $(__GO_MK_DIST_BIN)
# would pay the gate twice on every real change, because the engine's install
# command runs the gate and the compile itself. Running `make build install`
# together still gates twice; either one alone gates once.
build: $(__GO_MK_DIST_OUTPUTS)

$(__GO_MK_DIST_FIRST_OUTPUT): $(__GO_MK_FRESHNESS_INPUTS) $(__GO_MK_SETTINGS_STAMP) | go-mk-bin
	@"$(__GO_MK_BIN_RESOLVED)" build

deploy: install

install: $(__GO_MK_INSTALL_OUTPUTS)

$(__GO_MK_INSTALL_FIRST_OUTPUT): $(__GO_MK_FRESHNESS_INPUTS) $(__GO_MK_SETTINGS_STAMP) | go-mk-bin
	@"$(__GO_MK_BIN_RESOLVED)" install

# An install hook is a side effect the consumer expects on every install, not
# a step whose result is the installed file. Declaring one opts that repo out
# of skipping, so a hook that restarts a service or publishes an artifact still
# runs when the binary is already current.
ifneq ($(strip $(GO_MK_INSTALL_PRE_CMD)$(GO_MK_INSTALL_POST_CMD)),)
.PHONY: __go-mk-install-hooks-declared
__go-mk-install-hooks-declared:
	@:

$(__GO_MK_INSTALL_FIRST_OUTPUT): __go-mk-install-hooks-declared
endif

# The primary rule writes every declared binary, so a secondary one is normally
# already in place and no older than the primary by the time make reaches it.
# The guard covers a secondary that was deleted or left behind while the
# primary was current, and it runs the engine at most once, because one call
# rewrites all of them and the engine writes the primary first.
__go-mk-output-is-current = test -x '$(2)' && test -z "$$(find '$(1)' -newer '$(2)' 2>/dev/null)"

$(__GO_MK_DIST_OTHER_OUTPUTS): $(__GO_MK_DIST_FIRST_OUTPUT) | go-mk-bin
	@$(call __go-mk-output-is-current,$(__GO_MK_DIST_FIRST_OUTPUT),$@) || "$(__GO_MK_BIN_RESOLVED)" build

$(__GO_MK_INSTALL_OTHER_OUTPUTS): $(__GO_MK_INSTALL_FIRST_OUTPUT) | go-mk-bin
	@$(call __go-mk-output-is-current,$(__GO_MK_INSTALL_FIRST_OUTPUT),$@) || "$(__GO_MK_BIN_RESOLVED)" install

uninstall: | go-mk-bin
	@"$(__GO_MK_BIN_RESOLVED)" uninstall

version-info:
	@echo "binary:      $(BINARY)"
	@echo "cmd:         $(CMD)"
	@echo "vpkg:        $(VPKG)"
	@echo "gklog_vpkg:  $(GKLOG_VPKG)"
	@echo "commit:      $(__GO_MK_GIT_COMMIT)"
	@echo "version:     $(__GO_MK_GIT_VERSION)"
	@echo "dirty:       $(__GO_MK_GIT_DIRTY)"
	@echo "build_time:  $(__GO_MK_BUILD_TIME)"
	@echo "tags:        $(GO_BUILD_TAGS)"
	@echo "cgo_enabled: $(CGO_ENABLED)"
	@echo "codesign_entitlements: $(CODESIGN_ENTITLEMENTS)"
	@echo "install_dir: $(INSTALL_DIR)"

clean-dist:
	@rm -rf $(DIST_DIR)
	@echo "cleaned: $(DIST_DIR)"

# Targets that actually compile, for the __GO_MK_PREREQS attachment below. These
# are the file rules, not the build/install wrappers: make orders a target's
# recipe after its prerequisites, but leaves the order among those
# prerequisites unspecified, so codegen hung off the wrapper could run after
# the compile it is supposed to precede.
__GO_MK_COMPILE_TARGETS := $(__GO_MK_DIST_OUTPUTS) $(__GO_MK_INSTALL_OUTPUTS)

endif

# __GO_MK_PREREQS (see go.mk): codegen and go.work routing. Attach to this
# module's compile targets so a consumer that opts into go-build.mk also
# generates its parsers/proto and materializes go.work before build and install.
# Empty default is a no-op.
ifneq ($(strip $(__GO_MK_PREREQS)),)
$(__GO_MK_COMPILE_TARGETS): | $(__GO_MK_PREREQS)
endif
