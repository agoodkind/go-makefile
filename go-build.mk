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
# Targets exposed: build, install, uninstall, version-info, clean-dist.
# Override go.mk's `build`/`deploy`/`clean` defaults with the standardized flow.
#
# Strict staticcheck-extra is the default for every consumer that opts into
# this module. Projects can soften via STATICCHECK_EXTRA_FLAGS in their own
# Makefile if needed.
STATICCHECK_EXTRA_FLAGS ?= $(STATICCHECK_EXTRA_CORE_FLAGS) $(STATICCHECK_EXTRA_STRICT_FLAGS)

.PHONY: build install uninstall version-info clean-dist

# Auto-detect mode. LIBRARY mode skips build/install (lint/test/vet still apply
# from go.mk). The default is binary mode, requiring BINARY+CMD+VPKG.
LIBRARY ?=

ifeq ($(strip $(LIBRARY)),1)

# Library mode: no binary to produce, but lint/vet/govulncheck still gate.
build: $(default-build-deps)
	@echo "library mode: no binary to build"

install:
	@echo "library mode: install is a no-op"

uninstall:
	@echo "library mode: uninstall is a no-op"

version-info:
	@echo "library mode: no version stamping"

clean-dist:
	@:

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
ifneq ($(strip $(VPKG)),)
GO_BUILD_LDFLAGS += \
	-X $(VPKG).Commit=$(GIT_COMMIT) \
	-X $(VPKG).Version=$(GIT_VERSION) \
	-X $(VPKG).Dirty=$(GIT_DIRTY) \
	-X $(VPKG).BuildTime=$(BUILD_TIME)
endif

ifneq ($(strip $(GKLOG_VPKG)),)
GO_BUILD_LDFLAGS += \
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
CODESIGN_ENTITLEMENTS_FLAG := $(if $(strip $(CODESIGN_ENTITLEMENTS)),--entitlements "$(CODESIGN_ENTITLEMENTS)",)

define codesign_binary
	@if [ "$$(uname)" = "Darwin" ]; then \
		if [ -z "$(CODESIGN_IDENTITY)" ]; then \
			echo "No Developer ID Application signing identity found."; \
			echo "Set CERT_ID in config.mk or install a Developer ID Application certificate."; \
			exit 1; \
		fi; \
		echo "Signing $(1) with $(CODESIGN_IDENTITY)..."; \
		codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(BUNDLE_ID)" --options runtime --timestamp=$(CODESIGN_TIMESTAMP) $(CODESIGN_ENTITLEMENTS_FLAG) "$(1)"; \
		codesign --verify --verbose=2 "$(1)"; \
	fi
endef

# Build runs build-check (vet+lint+govulncheck) from go.mk first, unless
# BUILD_CHECKS=false is set. On macOS, the resulting binary is signed in
# place before install copies it.
build: $(default-build-deps)
	@mkdir -p $(DIST_DIR)
	go build $(GO_BUILD_FLAGS) -o $(DIST_BIN) $(CMD)
	@echo "built: $(DIST_BIN)"
	$(call codesign_binary,$(DIST_BIN))

# Atomic install to $(INSTALL_BIN) via mktemp + rename. Avoids a torn binary
# if the cp is interrupted mid-write.
install: build
	@mkdir -p $(INSTALL_DIR)
	@out="$$(mktemp $(INSTALL_BIN).new.XXXXXX)"; \
	trap 'rm -f "$$out"' EXIT; \
	cp -f "$(DIST_BIN)" "$$out"; \
	chmod 0755 "$$out"; \
	test -s "$$out"; \
	mv -f "$$out" "$(INSTALL_BIN)"
	@echo "installed: $(INSTALL_BIN)"

uninstall:
	@rm -f $(INSTALL_BIN)
	@echo "removed: $(INSTALL_BIN)"

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

endif
