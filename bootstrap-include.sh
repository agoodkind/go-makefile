#!/usr/bin/env bash
# bootstrap-include.sh: fetch go.mk plus opt-in sibling modules plus the
# central golangci.yml from go-makefile. Honors a local-checkout override,
# implements a per-asset cache TTL, and authenticates the GitHub Contents
# API call when a token is available.
#
# Each consumer Makefile invokes this script once at parse time. The script
# populates .make/ with the files the rest of the build expects.
#
# Cross-platform note: we deliberately avoid stat and date because their
# flags differ between BSD (macOS) and GNU (Linux). The freshness check
# uses find -mmin which both implementations support with the same flag
# semantics. No timestamp arithmetic happens in the shell.
#
# Env config (all optional):
#   GO_MK_DEV_DIR     path to local go-makefile checkout. When set and the
#                     file exists, copy it instead of fetching.
#   GO_MK_MODULES     space-separated list of sibling .mk files to include
#                     (e.g. "go-build.mk go-release.mk go-service.mk").
#   GO_MK_TTL_MIN     freshness window in minutes; skip fetch when the
#                     target file was modified within the last N minutes.
#                     Default 5. Set to 0 to force re-fetch.
#   GITHUB_TOKEN      bearer token for the GitHub Contents API. Raises the
#                     rate limit from 60 per hour to 5000 per hour. When
#                     unset, falls back to `gh auth token` if the gh CLI
#                     is on PATH and authenticated; otherwise unauth.
#
# Internal env (override only when forking go-makefile):
#   GO_MK_BASE_URL    raw URL base.
#   GO_MK_API_BASE    GitHub Contents API base.
#   GO_MK_API_REF     API ref query value (default main).
#   GO_MK_CACHE_DIR   on-disk cache root (default $XDG_CACHE_HOME or ~/.cache).
#   GO_MK             local target path for go.mk (default .make/go.mk).

set -eu

GO_MK_BASE_URL="${GO_MK_BASE_URL:-https://raw.githubusercontent.com/agoodkind/go-makefile/main}"
GO_MK_API_BASE="${GO_MK_API_BASE:-https://api.github.com/repos/agoodkind/go-makefile/contents}"
GO_MK_API_REF="${GO_MK_API_REF:-main}"
GO_MK_DEV_DIR="${GO_MK_DEV_DIR:-}"
GO_MK_TTL_MIN="${GO_MK_TTL_MIN:-5}"
GO_MK_CACHE_DIR="${GO_MK_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/go-makefile}"
GO_MK="${GO_MK:-.make/go.mk}"
GO_MK_MODULES="${GO_MK_MODULES:-}"

mkdir -p "$(dirname "$GO_MK")" "$GO_MK_CACHE_DIR"

# Resolve a GitHub auth token. Env first so CI environments stay explicit;
# fall back to `gh auth token` for local-dev convenience.
auth_token=""
if [ -n "${GITHUB_TOKEN:-}" ]; then
    auth_token="$GITHUB_TOKEN"
elif command -v gh >/dev/null 2>&1; then
    auth_token="$(gh auth token 2>/dev/null || true)"
fi

# file_is_fresh returns 0 when the file exists and was modified within the
# TTL window. find -mmin is supported on both BSD (macOS) and GNU (Linux)
# with identical semantics, which avoids the stat -f vs stat -c portability
# split.
file_is_fresh() {
    local f="$1"
    local ttl_min="$2"
    [ -f "$f" ] || return 1
    [ "$ttl_min" -ge 1 ] 2>/dev/null || return 1
    [ -n "$(find "$f" -mmin -"$ttl_min" 2>/dev/null)" ]
}

# try_fetch downloads <rel> into <tmp>. Two tiers run in order: the GitHub
# Contents API (with auth when a token is available) and the plain raw URL.
# A 403 from the rate-limited API path falls through to raw without leaking
# stderr.
try_fetch() {
    local tmp="$1" rel="$2"
    local api_url="$GO_MK_API_BASE/$rel?ref=$GO_MK_API_REF"
    local raw_url="$GO_MK_BASE_URL/$rel"

    if [ -n "$auth_token" ]; then
        if curl -fsSL \
                -H "Authorization: Bearer $auth_token" \
                -H "Accept: application/vnd.github.raw" \
                --connect-timeout 5 --max-time 10 \
                "$api_url" -o "$tmp" 2>/dev/null; then
            return 0
        fi
    else
        if curl -fsSL \
                -H "Accept: application/vnd.github.raw" \
                --connect-timeout 5 --max-time 10 \
                "$api_url" -o "$tmp" 2>/dev/null; then
            return 0
        fi
    fi

    if curl -fsSL --connect-timeout 5 --max-time 10 \
            "$raw_url" -o "$tmp" 2>/dev/null; then
        return 0
    fi

    return 1
}

# fetch_one populates a single asset, applying override and TTL rules.
#
# TODO(moratorium): the on-disk cache fallback was removed because a stale
# cache silently masked an upstream go.mk breakage and froze every consumer
# on a broken pipeline. Restore the cache only after the primary fetch path
# (gh-auth + raw fallback) has been demonstrably reliable for a sustained
# period. Until then, fail loud rather than serve stale.
fetch_one() {
    local rel="$1" target="$2"
    mkdir -p "$(dirname "$target")"

    if [ -n "$GO_MK_DEV_DIR" ] && [ -f "$GO_MK_DEV_DIR/$rel" ]; then
        cp "$GO_MK_DEV_DIR/$rel" "$target"
        return 0
    fi

    if file_is_fresh "$target" "$GO_MK_TTL_MIN"; then
        return 0
    fi

    local tmp="$target.tmp"
    if try_fetch "$tmp" "$rel" && [ -s "$tmp" ]; then
        mv "$tmp" "$target"
        return 0
    fi

    rm -f "$tmp"
    printf '%s\n' "error: $rel fetch failed (no cache fallback; moratorium). Run: gh auth login" >&2
    return 1
}

fetch_one "go.mk" "$GO_MK"
fetch_one "golangci.yml" ".make/golangci.yml" || true
for m in $GO_MK_MODULES; do
    [ -n "$m" ] && fetch_one "$m" ".make/$m" || true
done
