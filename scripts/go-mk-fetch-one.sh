#!/usr/bin/env bash
set -eo pipefail

# Fetch one go-makefile asset to a destination path. Resolution order: a developer
# directory override, then the raw CDN (cache-busted first, then plain). The gh api
# contents path was removed; the tarball prime in bootstrap.mk and go.mk is the
# primary fetch now, so this script is the per-file fallback.

relative_path="${1:?relative path is required}"
destination_path="${2:?destination path is required}"
developer_dir="${3:-${GO_MK_DEV_DIR:-}}"
base_url="${GO_MK_BASE_URL:-https://raw.githubusercontent.com/agoodkind/go-makefile/main}"

mkdir -p "$(dirname "${destination_path}")"
temporary_path=$(mktemp "${destination_path}.tmp.XXXXXX")
error_path=$(mktemp "${destination_path}.err.XXXXXX")

cleanup() {
    rm -f "${temporary_path}" "${error_path}"
}

trap cleanup EXIT

copy_from_developer_dir() {
    if [[ -z "${developer_dir}" || ! -f "${developer_dir}/${relative_path}" ]]; then
        return 1
    fi
    cp "${developer_dir}/${relative_path}" "${temporary_path}"
}

fetch_from_url() {
    local url="$1"
    curl -fsSL --connect-timeout 5 --max-time 10 --retry 3 --retry-delay 2 \
        "${url}" -o "${temporary_path}" 2>"${error_path}"
}

fetch_from_raw_cdn() {
    # Cache-bust first so a stale CDN copy cannot win, then fall back to the plain
    # URL in case the query string is rejected.
    local cache_bust="${EPOCHSECONDS:-$(date +%s)}"
    fetch_from_url "${base_url}/${relative_path}?v=${cache_bust}" ||
        fetch_from_url "${base_url}/${relative_path}"
}

if copy_from_developer_dir || fetch_from_raw_cdn; then
    if [[ ! -s "${temporary_path}" ]]; then
        printf "error: %s fetched empty body\n" "${relative_path}"
        exit 1
    fi
    mv "${temporary_path}" "${destination_path}"
    case "${destination_path}" in
        *.sh) chmod +x "${destination_path}" ;;
    esac
    exit 0
fi

printf "error: %s fetch failed; no cache fallback. Set GO_MK_DEV_DIR or check network access to %s\n" \
    "${relative_path}" "${base_url}"
exit 1
