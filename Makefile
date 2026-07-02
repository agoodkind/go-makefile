# go-makefile's own Makefile. This repo is the source of the central
# pipeline every other repo consumes. It eats its own dog food by running
# go.mk against itself. Run `make help` for the canonical entry points
# (build/check/lint/fmt) and per-linter sub-targets.
#
# Layout: three Go modules in this tree.
#   .            render.go (bootstrap renderer) plus the project README/configs.
#   staticcheck/ the custom analyzer set (clyde-staticcheck-style strict checks).
#   selfupdate/  the reusable updater library for consumer CLIs.
# Each gate runs the central go.mk pipeline against all modules.

GO_MK := go.mk

# Canonical golangci config lives in this repo at golangci.yml.
ROOT_LINT_ARGS  := GOLANGCI_LINT_FLAGS="-c golangci.yml" GOLANGCI_LINT_TARGETS=.
STATIC_LINT_ARGS := GOLANGCI_LINT_FLAGS="-c ../golangci.yml" GOLANGCI_LINT_TARGETS=./...
SELFUPDATE_LINT_ARGS := GOLANGCI_LINT_FLAGS="-c ../golangci.yml" GOLANGCI_LINT_TARGETS=./...

# Dog-food: build the staticcheck-extra binary from this checkout instead of
# `go install ...@latest`, so analyzer changes are exercised before push.
STATICCHECK_EXTRA_LOCAL_ARGS := \
	STATICCHECK_EXTRA_BUILD_REPO="$(CURDIR)/staticcheck" \
	STATICCHECK_EXTRA_BUILD_PKG=./cmd/staticcheck-extra
ROOT_LINT_ARGS  += $(STATICCHECK_EXTRA_LOCAL_ARGS)
STATIC_LINT_ARGS += $(STATICCHECK_EXTRA_LOCAL_ARGS)
SELFUPDATE_LINT_ARGS += $(STATICCHECK_EXTRA_LOCAL_ARGS)

# Dog-food the go-mk engine binary from this checkout (the root module)
# for both the root and static sub-makes, so engine changes are exercised before
# push and neither sub-make resolves @main over the network.
GO_MK_LOCAL_ARGS := \
	GO_MK_DEV_DIR="$(CURDIR)" \
	GO_MK_BUILD_REPO="$(CURDIR)" \
	GO_MK_BUILD_PKG=./cmd/go-mk
ROOT_LINT_ARGS  += $(GO_MK_LOCAL_ARGS)
STATIC_LINT_ARGS += $(GO_MK_LOCAL_ARGS)
SELFUPDATE_LINT_ARGS += $(GO_MK_LOCAL_ARGS)

ROOT_GO_MK   := $(MAKE) -f $(GO_MK) $(ROOT_LINT_ARGS)
STATIC_GO_MK := $(MAKE) -C staticcheck -f ../$(GO_MK) $(STATIC_LINT_ARGS)
SELFUPDATE_GO_MK := $(MAKE) -C selfupdate -f ../$(GO_MK) $(SELFUPDATE_LINT_ARGS)

.DEFAULT_GOAL := check

.PHONY: build release check lint fmt vet test govulncheck go-version-check build-check ci-changed go-mk-cache-manifest \
        lint-tools lint-golangci lint-files lint-diff lint-format lint-gocyclo lint-deadcode staticcheck-extra \
        lint-golangci-baseline lint-golangci-baseline-prune-fixed lint-golangci-baseline-remove-fixed lint-golangci-baseline-accept-new \
        lint-gocyclo-baseline lint-gocyclo-baseline-prune-fixed lint-gocyclo-baseline-remove-fixed lint-gocyclo-baseline-accept-new \
        lint-deadcode-baseline lint-deadcode-baseline-prune-fixed lint-deadcode-baseline-remove-fixed lint-deadcode-baseline-accept-new \
        staticcheck-extra-baseline staticcheck-extra-baseline-prune-fixed staticcheck-extra-baseline-remove-fixed staticcheck-extra-baseline-accept-new \
        baseline baseline-prune-fixed baseline-remove-fixed baseline-accept-new baseline-add-new \
        go-mk-sync update-go-mk smoke-fetch help

# Each gate target runs the central go.mk recipe once per Go module.

build:
	$(ROOT_GO_MK) build
	$(STATIC_GO_MK) build
	$(SELFUPDATE_GO_MK) build

release:
	$(ROOT_GO_MK) GO_MK_MODULES=go-release.mk BINARY=go-mk CMD=./cmd/go-mk release

build-check:
	$(ROOT_GO_MK) build-check
	$(STATIC_GO_MK) build-check
	$(SELFUPDATE_GO_MK) build-check

lint:
	$(ROOT_GO_MK) lint
	$(STATIC_GO_MK) lint
	$(SELFUPDATE_GO_MK) lint

lint-tools:
	$(ROOT_GO_MK) lint-tools

lint-golangci:
	$(ROOT_GO_MK) lint-golangci
	$(STATIC_GO_MK) lint-golangci
	$(SELFUPDATE_GO_MK) lint-golangci

lint-golangci-baseline:
	$(ROOT_GO_MK) lint-golangci-baseline
	$(STATIC_GO_MK) lint-golangci-baseline
	$(SELFUPDATE_GO_MK) lint-golangci-baseline

lint-golangci-baseline-prune-fixed:
	$(ROOT_GO_MK) lint-golangci-baseline-prune-fixed
	$(STATIC_GO_MK) lint-golangci-baseline-prune-fixed
	$(SELFUPDATE_GO_MK) lint-golangci-baseline-prune-fixed

lint-golangci-baseline-remove-fixed: lint-golangci-baseline-prune-fixed

lint-golangci-baseline-accept-new:
	$(ROOT_GO_MK) lint-golangci-baseline-accept-new
	$(STATIC_GO_MK) lint-golangci-baseline-accept-new
	$(SELFUPDATE_GO_MK) lint-golangci-baseline-accept-new

# lint-files runs golangci-lint scoped to LINT_FILES against the root module
# only (file-scoped lints in the staticcheck submodule require a different
# working directory). When iterating on the analyzer, pass LINT_FILES with
# the staticcheck/ paths and run from inside staticcheck/ explicitly.
lint-files:
	$(ROOT_GO_MK) lint-files

lint-diff:
	$(ROOT_GO_MK) lint-diff

lint-format:
	$(ROOT_GO_MK) lint-format
	$(STATIC_GO_MK) lint-format
	$(SELFUPDATE_GO_MK) lint-format

lint-gocyclo:
	$(ROOT_GO_MK) lint-gocyclo
	$(STATIC_GO_MK) lint-gocyclo
	$(SELFUPDATE_GO_MK) lint-gocyclo

lint-gocyclo-baseline:
	$(ROOT_GO_MK) lint-gocyclo-baseline
	$(STATIC_GO_MK) lint-gocyclo-baseline
	$(SELFUPDATE_GO_MK) lint-gocyclo-baseline

lint-gocyclo-baseline-prune-fixed:
	$(ROOT_GO_MK) lint-gocyclo-baseline-prune-fixed
	$(STATIC_GO_MK) lint-gocyclo-baseline-prune-fixed
	$(SELFUPDATE_GO_MK) lint-gocyclo-baseline-prune-fixed

lint-gocyclo-baseline-remove-fixed: lint-gocyclo-baseline-prune-fixed

lint-gocyclo-baseline-accept-new:
	$(ROOT_GO_MK) lint-gocyclo-baseline-accept-new
	$(STATIC_GO_MK) lint-gocyclo-baseline-accept-new
	$(SELFUPDATE_GO_MK) lint-gocyclo-baseline-accept-new

lint-deadcode:
	$(ROOT_GO_MK) lint-deadcode
	$(STATIC_GO_MK) lint-deadcode
	$(SELFUPDATE_GO_MK) lint-deadcode

lint-deadcode-baseline:
	$(ROOT_GO_MK) lint-deadcode-baseline
	$(STATIC_GO_MK) lint-deadcode-baseline
	$(SELFUPDATE_GO_MK) lint-deadcode-baseline

lint-deadcode-baseline-prune-fixed:
	$(ROOT_GO_MK) lint-deadcode-baseline-prune-fixed
	$(STATIC_GO_MK) lint-deadcode-baseline-prune-fixed
	$(SELFUPDATE_GO_MK) lint-deadcode-baseline-prune-fixed

lint-deadcode-baseline-remove-fixed: lint-deadcode-baseline-prune-fixed

lint-deadcode-baseline-accept-new:
	$(ROOT_GO_MK) lint-deadcode-baseline-accept-new
	$(STATIC_GO_MK) lint-deadcode-baseline-accept-new
	$(SELFUPDATE_GO_MK) lint-deadcode-baseline-accept-new

staticcheck-extra:
	$(ROOT_GO_MK) staticcheck-extra
	$(STATIC_GO_MK) staticcheck-extra
	$(SELFUPDATE_GO_MK) staticcheck-extra

staticcheck-extra-baseline:
	$(ROOT_GO_MK) staticcheck-extra-baseline
	$(STATIC_GO_MK) staticcheck-extra-baseline
	$(SELFUPDATE_GO_MK) staticcheck-extra-baseline

staticcheck-extra-baseline-prune-fixed:
	$(ROOT_GO_MK) staticcheck-extra-baseline-prune-fixed
	$(STATIC_GO_MK) staticcheck-extra-baseline-prune-fixed
	$(SELFUPDATE_GO_MK) staticcheck-extra-baseline-prune-fixed

staticcheck-extra-baseline-remove-fixed: staticcheck-extra-baseline-prune-fixed

staticcheck-extra-baseline-accept-new:
	$(ROOT_GO_MK) staticcheck-extra-baseline-accept-new
	$(STATIC_GO_MK) staticcheck-extra-baseline-accept-new
	$(SELFUPDATE_GO_MK) staticcheck-extra-baseline-accept-new

fmt:
	$(ROOT_GO_MK) fmt
	$(STATIC_GO_MK) fmt
	$(SELFUPDATE_GO_MK) fmt

vet:
	$(ROOT_GO_MK) vet
	$(STATIC_GO_MK) vet
	$(SELFUPDATE_GO_MK) vet

test:
	$(ROOT_GO_MK) test
	$(STATIC_GO_MK) test
	$(SELFUPDATE_GO_MK) test

govulncheck:
	$(ROOT_GO_MK) govulncheck
	$(STATIC_GO_MK) govulncheck
	$(SELFUPDATE_GO_MK) govulncheck

go-version-check:
	$(ROOT_GO_MK) go-version-check
	$(STATIC_GO_MK) go-version-check
	$(SELFUPDATE_GO_MK) go-version-check

# ci-changed runs once against the root module. The detector diffs the whole repo
# and its source-extension fast path catches a .go change in either module, so a
# single invocation decides for the whole checkout.
ci-changed:
	$(ROOT_GO_MK) ci-changed

go-mk-cache-manifest:
	$(ROOT_GO_MK) go-mk-cache-manifest

baseline:
	$(ROOT_GO_MK) baseline
	$(STATIC_GO_MK) baseline
	$(SELFUPDATE_GO_MK) baseline

baseline-prune-fixed:
	$(ROOT_GO_MK) baseline-prune-fixed
	$(STATIC_GO_MK) baseline-prune-fixed
	$(SELFUPDATE_GO_MK) baseline-prune-fixed

baseline-remove-fixed: baseline-prune-fixed

baseline-accept-new:
	$(ROOT_GO_MK) baseline-accept-new
	$(STATIC_GO_MK) baseline-accept-new
	$(SELFUPDATE_GO_MK) baseline-accept-new

baseline-add-new: baseline-accept-new

go-mk-sync update-go-mk:
	$(ROOT_GO_MK) update-go-mk

smoke-fetch:
	$(ROOT_GO_MK) smoke-fetch

check: lint

help:
	$(ROOT_GO_MK) help
