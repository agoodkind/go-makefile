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

.PHONY: build install uninstall version-info clean-dist

# Auto-detect mode. LIBRARY mode skips build/install (lint/test/vet still apply
# from go.mk). The default is binary mode, requiring BINARY+CMD+VPKG.
LIBRARY ?=

ifeq ($(strip $(LIBRARY)),1)

build:
	@echo "library mode: build is a no-op"

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
ifeq ($(strip $(VPKG)),)
$(error go-build.mk: VPKG is not set)
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

# Stamped LDFLAGS. Project's VPKG must define matching string vars (Commit,
# Version, Dirty, BuildTime). If GKLOG_VPKG is set, cross-stamp gklog too.
GO_BUILD_LDFLAGS := \
	-X $(VPKG).Commit=$(GIT_COMMIT) \
	-X $(VPKG).Version=$(GIT_VERSION) \
	-X $(VPKG).Dirty=$(GIT_DIRTY) \
	-X $(VPKG).BuildTime=$(BUILD_TIME)

ifneq ($(strip $(GKLOG_VPKG)),)
GO_BUILD_LDFLAGS += \
	-X $(GKLOG_VPKG).Commit=$(GIT_COMMIT) \
	-X $(GKLOG_VPKG).Dirty=$(GIT_DIRTY) \
	-X $(GKLOG_VPKG).BuildTime=$(BUILD_TIME) \
	-X $(GKLOG_VPKG).BinHash=
endif

GO_BUILD_TAGS         ?=
GO_BUILD_TAGS_FLAG    := $(if $(strip $(GO_BUILD_TAGS)),-tags '$(GO_BUILD_TAGS)',)
GO_BUILD_EXTRA_FLAGS  ?=

# Override go.mk's GO_BUILD_FLAGS so its `build` target picks up our ldflags
# even when called via the legacy path. The standardized `build` below uses
# the same vars.
GO_BUILD_FLAGS := $(GO_BUILD_TAGS_FLAG) -ldflags '$(GO_BUILD_LDFLAGS)' $(GO_BUILD_EXTRA_FLAGS)

# Build runs build-check (vet+lint+govulncheck) from go.mk first, unless
# BUILD_CHECKS=false is set.
build: $(default-build-deps)
	@mkdir -p $(DIST_DIR)
	go build $(GO_BUILD_FLAGS) -o $(DIST_BIN) $(CMD)
	@echo "built: $(DIST_BIN)"

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
	@echo "install_dir: $(INSTALL_DIR)"

clean-dist:
	@rm -rf $(DIST_DIR)
	@echo "cleaned: $(DIST_DIR)"

endif
