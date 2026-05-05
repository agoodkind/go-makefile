# go-makefile's own Makefile. This repo is the source of the central
# pipeline every other repo consumes. It eats its own dog food by running
# go.mk against itself. Run `make help` for the canonical entry points
# (build/check/lint/fmt) and per-linter sub-targets.
#
# Layout: two Go modules in this tree.
#   .            render.go (bootstrap renderer) plus the project README/configs.
#   staticcheck/ the custom analyzer set (clyde-staticcheck-style strict checks).
# Each gate runs the central go.mk pipeline against both modules.

GO_MK := go.mk

# Canonical golangci config lives in this repo at golangci.yml.
# golangci-template.yml is the legacy name kept for backward compat with
# any pre-centralization consumer that still does `extends: ...template.yml`.
ROOT_LINT_ARGS  := GOLANGCI_LINT_FLAGS="-c golangci.yml" GOLANGCI_LINT_TARGETS=.
STATIC_LINT_ARGS := GOLANGCI_LINT_FLAGS="-c ../golangci.yml" GOLANGCI_LINT_TARGETS=./...

# Dog-food: build the staticcheck-extra binary from this checkout instead of
# `go install ...@latest`, so analyzer changes are exercised before push.
STATICCHECK_EXTRA_LOCAL_ARGS := \
	STATICCHECK_EXTRA_BUILD_REPO="$(CURDIR)/staticcheck" \
	STATICCHECK_EXTRA_BUILD_PKG=./cmd/staticcheck-extra
ROOT_LINT_ARGS  += $(STATICCHECK_EXTRA_LOCAL_ARGS)
STATIC_LINT_ARGS += $(STATICCHECK_EXTRA_LOCAL_ARGS)

ROOT_GO_MK   := $(MAKE) -f $(GO_MK) $(ROOT_LINT_ARGS)
STATIC_GO_MK := $(MAKE) -C staticcheck -f ../$(GO_MK) $(STATIC_LINT_ARGS)

.DEFAULT_GOAL := check

.PHONY: build check lint fmt vet test govulncheck build-check \
        lint-tools lint-golangci lint-files lint-format lint-gocyclo lint-deadcode staticcheck-extra \
        lint-golangci-baseline lint-deadcode-baseline staticcheck-extra-baseline \
        help

# Each gate target runs the central go.mk recipe twice, once per Go module.
# The static analyzer module runs with BUILD_CHECKS=false on build because
# it has no main package (would no-op anyway via go.mk's library handling)
# and we do not want it pulling its own build-check chain through this
# coordinating Makefile.

build:
	$(ROOT_GO_MK) build
	$(STATIC_GO_MK) BUILD_CHECKS=false build

build-check:
	$(ROOT_GO_MK) build-check
	$(STATIC_GO_MK) build-check

lint:
	$(ROOT_GO_MK) lint
	$(STATIC_GO_MK) lint

lint-tools:
	$(ROOT_GO_MK) lint-tools

lint-golangci:
	$(ROOT_GO_MK) lint-golangci
	$(STATIC_GO_MK) lint-golangci

lint-golangci-baseline:
	$(ROOT_GO_MK) lint-golangci-baseline
	$(STATIC_GO_MK) lint-golangci-baseline

# lint-files runs golangci-lint scoped to LINT_FILES against the root module
# only (file-scoped lints in the staticcheck submodule require a different
# working directory). When iterating on the analyzer, pass LINT_FILES with
# the staticcheck/ paths and run from inside staticcheck/ explicitly.
lint-files:
	$(ROOT_GO_MK) lint-files

lint-format:
	$(ROOT_GO_MK) lint-format
	$(STATIC_GO_MK) lint-format

lint-gocyclo:
	$(ROOT_GO_MK) lint-gocyclo

lint-deadcode:
	$(ROOT_GO_MK) lint-deadcode
	$(STATIC_GO_MK) lint-deadcode

lint-deadcode-baseline:
	$(ROOT_GO_MK) lint-deadcode-baseline
	$(STATIC_GO_MK) lint-deadcode-baseline

staticcheck-extra:
	$(ROOT_GO_MK) staticcheck-extra
	$(STATIC_GO_MK) staticcheck-extra

staticcheck-extra-baseline:
	$(ROOT_GO_MK) staticcheck-extra-baseline
	$(STATIC_GO_MK) staticcheck-extra-baseline

fmt:
	$(ROOT_GO_MK) fmt
	$(STATIC_GO_MK) fmt

vet:
	$(ROOT_GO_MK) vet
	$(STATIC_GO_MK) vet

test:
	$(ROOT_GO_MK) test
	$(STATIC_GO_MK) test

govulncheck:
	$(ROOT_GO_MK) govulncheck
	$(STATIC_GO_MK) govulncheck

check: build test

help:
	$(ROOT_GO_MK) help
