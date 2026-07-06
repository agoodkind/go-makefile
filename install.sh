#!/usr/bin/env bash
# shellcheck shell=bash
set -euo pipefail

readonly GO_MK_REPO="agoodkind/go-makefile"
readonly GO_MK_RELEASES_URL="https://github.com/${GO_MK_REPO}/releases"
readonly GO_MK_API_RELEASES_URL="https://api.github.com/repos/${GO_MK_REPO}/releases?per_page=20"

usage() {
    printf 'usage: install.sh --repo OWNER/NAME --binary NAME [--bin-dir DIR] [--version TAG] [--require-attestation] [-- <post-install args...>]\n' >&2
}

fail() {
    printf 'install.sh: %s\n' "$1" >&2
    exit 1
}

detect_os() {
    local kernel_name
    kernel_name="$(uname -s)"
    case "$kernel_name" in
        Darwin)
            printf 'darwin\n'
            ;;
        Linux)
            printf 'linux\n'
            ;;
        *)
            fail "unsupported operating system: ${kernel_name}"
            ;;
    esac
}

detect_arch() {
    local machine_name
    machine_name="$(uname -m)"
    case "$machine_name" in
        arm64 | aarch64)
            printf 'arm64\n'
            ;;
        x86_64 | amd64)
            printf 'amd64\n'
            ;;
        *)
            fail "unsupported architecture: ${machine_name}"
            ;;
    esac
}

curl_auth_args() {
    if [[ -n "${GITHUB_TOKEN:-}" ]]; then
        printf '%s\n' "-H"
        printf '%s\n' "Authorization: Bearer ${GITHUB_TOKEN}"
    fi
}

download_file() {
    local url="$1"
    local output_path="$2"
    local auth_args=()
    while IFS= read -r arg; do
        auth_args+=("$arg")
    done < <(curl_auth_args)
    curl -fsSL "${auth_args[@]}" -o "$output_path" "$url"
}

fetch_releases_json() {
    local auth_args=()
    while IFS= read -r arg; do
        auth_args+=("$arg")
    done < <(curl_auth_args)
    curl -fsSL "${auth_args[@]}" "$GO_MK_API_RELEASES_URL"
}

resolve_latest_go_mk_tag() {
    local line
    local releases
    local tag=""
    releases="$(fetch_releases_json)"
    while IFS= read -r line; do
        if [[ "$line" == *'"tag_name":'* ]]; then
            tag="${line#*\"tag_name\":}"
            tag="${tag#*\"}"
            tag="${tag%%\"*}"
        fi
        if [[ "$line" == *'"draft": false'* && -n "$tag" ]]; then
            printf '%s\n' "$tag"
            return
        fi
        if [[ "$line" == *'}'* ]]; then
            tag=""
        fi
    done <<< "$releases"
    fail "could not resolve latest ${GO_MK_REPO} release"
}

compute_sha256() {
    local path="$1"
    local digest
    if command -v sha256sum >/dev/null 2>&1; then
        read -r digest _ < <(sha256sum "$path")
        printf '%s\n' "$digest"
        return
    fi
    if command -v shasum >/dev/null 2>&1; then
        read -r digest _ < <(shasum -a 256 "$path")
        printf '%s\n' "$digest"
        return
    fi
    fail "sha256sum or shasum is required for checksum fallback"
}

checksum_for_asset() {
    local checksums_path="$1"
    local asset_name="$2"
    local digest
    local name
    while read -r digest name; do
        if [[ "$name" == "$asset_name" ]]; then
            printf '%s\n' "$digest"
            return
        fi
    done < "$checksums_path"
    fail "checksums.txt did not include ${asset_name}"
}

verify_checksum() {
    local checksums_path="$1"
    local asset_path="$2"
    local asset_name="$3"
    local expected_digest
    local actual_digest
    expected_digest="$(checksum_for_asset "$checksums_path" "$asset_name")"
    actual_digest="$(compute_sha256 "$asset_path")"
    if [[ "$expected_digest" != "$actual_digest" ]]; then
        fail "checksum mismatch for ${asset_name}"
    fi
    printf 'verified checksum: %s\n' "$asset_name" >&2
}

verify_go_mk_install_asset() {
    local tag="$1"
    local tarball_path="$2"
    local checksums_path="$3"
    local asset_name="$4"
    local require_attestation="$5"
    local gh_token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
    if command -v gh >/dev/null 2>&1; then
        if GH_TOKEN="$gh_token" gh release verify-asset "$tag" "$tarball_path" --repo "$GO_MK_REPO"; then
            return
        fi
        printf 'warning: gh release verify-asset failed for %s; using checksum fallback\n' "$asset_name" >&2
    else
        printf 'warning: gh is missing; using checksum fallback for %s\n' "$asset_name" >&2
    fi
    if [[ "$require_attestation" == "1" ]]; then
        fail "attestation verification required for ${asset_name}"
    fi
    verify_checksum "$checksums_path" "$tarball_path" "$asset_name"
}

REPO=""
BINARY=""
BIN_DIR="${XDG_BIN_HOME:-${HOME}/.local/bin}"
VERSION=""
REQUIRE_ATTESTATION="0"
POST_INSTALL_ARGS=()

while (($# > 0)); do
    case "$1" in
        --repo)
            (($# >= 2)) || fail "--repo requires a value"
            REPO="$2"
            shift 2
            ;;
        --binary)
            (($# >= 2)) || fail "--binary requires a value"
            BINARY="$2"
            shift 2
            ;;
        --bin-dir)
            (($# >= 2)) || fail "--bin-dir requires a value"
            BIN_DIR="$2"
            shift 2
            ;;
        --version)
            (($# >= 2)) || fail "--version requires a value"
            VERSION="$2"
            shift 2
            ;;
        --require-attestation)
            REQUIRE_ATTESTATION="1"
            shift
            ;;
        --)
            shift
            POST_INSTALL_ARGS=("$@")
            break
            ;;
        -h | --help)
            usage
            exit 0
            ;;
        *)
            usage
            fail "unknown argument: $1"
            ;;
    esac
done

[[ -n "$REPO" ]] || fail "--repo is required"
[[ -n "$BINARY" ]] || fail "--binary is required"

TMP_DIR="$(mktemp -d)"
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

OS_NAME="$(detect_os)"
ARCH_NAME="$(detect_arch)"
GO_MK_TAG="$(resolve_latest_go_mk_tag)"
GO_MK_INSTALL_ASSET="go-mk-install_${OS_NAME}_${ARCH_NAME}.tar.gz"
GO_MK_INSTALL_TARBALL="${TMP_DIR}/${GO_MK_INSTALL_ASSET}"
CHECKSUMS_PATH="${TMP_DIR}/checksums.txt"

download_file "${GO_MK_RELEASES_URL}/download/${GO_MK_TAG}/${GO_MK_INSTALL_ASSET}" "$GO_MK_INSTALL_TARBALL"
download_file "${GO_MK_RELEASES_URL}/download/${GO_MK_TAG}/checksums.txt" "$CHECKSUMS_PATH"
verify_go_mk_install_asset "$GO_MK_TAG" "$GO_MK_INSTALL_TARBALL" "$CHECKSUMS_PATH" "$GO_MK_INSTALL_ASSET" "$REQUIRE_ATTESTATION"

tar -xzf "$GO_MK_INSTALL_TARBALL" -C "$TMP_DIR"
GO_MK_INSTALL_BIN="${TMP_DIR}/go-mk-install"
[[ -x "$GO_MK_INSTALL_BIN" ]] || fail "extracted archive did not contain executable go-mk-install"

INSTALL_ARGS=(
    --repo "$REPO"
    --binary "$BINARY"
    --bin-dir "$BIN_DIR"
)
if [[ -n "$VERSION" ]]; then
    INSTALL_ARGS+=(--version "$VERSION")
fi
if [[ "$REQUIRE_ATTESTATION" == "1" ]]; then
    INSTALL_ARGS+=(--require-attestation)
fi
if ((${#POST_INSTALL_ARGS[@]} > 0)); then
    INSTALL_ARGS+=(--)
    INSTALL_ARGS+=("${POST_INSTALL_ARGS[@]}")
fi

"$GO_MK_INSTALL_BIN" "${INSTALL_ARGS[@]}"
