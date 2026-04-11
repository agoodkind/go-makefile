#!/usr/bin/env bash
set -euo pipefail

BASE_URL="https://raw.githubusercontent.com/agoodkind/go-makefile/main"

warn() { echo "warning: $*" >&2; }
die() { echo "error: $*" >&2; exit 1; }
skip() { echo "skipping $1 (already exists)"; }

# Require go.mod
[ -f go.mod ] || die "go.mod not found — run from project root after 'go mod init'"

# Infer module path and binary name from go.mod
MODULE=$(awk '/^module / { print $2; exit }' go.mod)
[ -n "$MODULE" ] || die "could not read module from go.mod"

# Binary = last path segment of module (e.g. goodkind.io/agent-gate -> agent-gate)
BINARY="${MODULE##*/}"

# Infer cmd path
if [ -d "cmd/$BINARY" ]; then
    CMD="./cmd/$BINARY"
elif [ -d "cmd" ] && [ "$(ls -1 cmd | wc -l)" -eq 1 ]; then
    ONLY=$(ls cmd)
    CMD="./cmd/$ONLY"
    BINARY="$ONLY"
else
    CMD="./cmd/$BINARY"
fi

echo "module:  $MODULE"
echo "binary:  $BINARY"
echo "cmd:     $CMD"
echo ""

# --- Makefile ---
if [ -f Makefile ]; then
    skip Makefile
else
    cat > Makefile <<MAKEFILE
GO_MK_URL   := $BASE_URL/go.mk
GO_MK       := .make/go.mk
GO_MK_CACHE := \$(HOME)/.cache/go-makefile/go.mk

\$(GO_MK):
	@mkdir -p \$(dir \$@)
	@if curl -fsSL --connect-timeout 5 --max-time 10 "\$(GO_MK_URL)" -o "\$@"; then \\
		mkdir -p "\$(dir \$(GO_MK_CACHE))" && cp "\$@" "\$(GO_MK_CACHE)"; \\
	elif [ -f "\$(GO_MK_CACHE)" ]; then \\
		echo "warning: go.mk fetch failed, using cached version" >&2; \\
		cp "\$(GO_MK_CACHE)" "\$@"; \\
	else \\
		echo "error: go.mk fetch failed and no cache available" >&2; \\
		exit 1; \\
	fi

-include \$(GO_MK)

.PHONY: sync
sync:
	@mkdir -p "\$(dir \$(GO_MK))"
	@if curl -fsSL --connect-timeout 5 --max-time 10 "\$(GO_MK_URL)" -o "\$(GO_MK)"; then \\
		mkdir -p "\$(dir \$(GO_MK_CACHE))" && cp "\$(GO_MK)" "\$(GO_MK_CACHE)"; \\
		echo "go.mk updated"; \\
	else \\
		echo "error: go.mk fetch failed" >&2; \\
		exit 1; \\
	fi

BINARY := $BINARY
CMD    := $CMD

.DEFAULT_GOAL := check

.PHONY: build deploy clean

build:
	go build \$(CMD)

deploy:
	go install \$(CMD)

clean:
	rm -f \$(BINARY)
MAKEFILE
    echo "created Makefile"
fi

# --- .golangci.yml ---
if [ -f .golangci.yml ]; then
    skip .golangci.yml
else
    cat > .golangci.yml <<GOLANGCI
# Extends the shared agoodkind golangci config.
# Add project-specific overrides below.
extends:
  - $BASE_URL/golangci-template.yml
GOLANGCI
    echo "created .golangci.yml"
fi

# --- .goreleaser.yaml ---
if [ -f .goreleaser.yaml ]; then
    skip .goreleaser.yaml
else
    curl -fsSL "$BASE_URL/goreleaser-template.yaml" \
        | sed "s/BINARY/$BINARY/g" \
        > .goreleaser.yaml
    echo "created .goreleaser.yaml"
fi

# --- .gitignore ---
if [ -f .gitignore ]; then
    if ! grep -q "^\.make/" .gitignore; then
        echo ".make/" >> .gitignore
        echo "added .make/ to .gitignore"
    fi
else
    echo ".make/" > .gitignore
    echo "created .gitignore"
fi

# --- .github/workflows/ci.yml ---
if [ -f .github/workflows/ci.yml ]; then
    skip .github/workflows/ci.yml
else
    mkdir -p .github/workflows
    cat > .github/workflows/ci.yml <<CIYML
name: CI
on: [push, pull_request]
jobs:
  ci:
    uses: agoodkind/go-makefile/.github/workflows/_ci.yml@main
    permissions:
      contents: read
CIYML
    echo "created .github/workflows/ci.yml"
fi

# --- .github/workflows/release.yml ---
if [ -f .github/workflows/release.yml ]; then
    skip .github/workflows/release.yml
else
    mkdir -p .github/workflows
    cat > .github/workflows/release.yml <<RELEASEYML
name: Release
on:
  push:
    branches: [main]
concurrency:
  group: release
  cancel-in-progress: true
jobs:
  release:
    uses: agoodkind/go-makefile/.github/workflows/_release.yml@main
    permissions:
      contents: write
    secrets: inherit
RELEASEYML
    echo "created .github/workflows/release.yml"
fi

echo ""
echo "done. next: make"
