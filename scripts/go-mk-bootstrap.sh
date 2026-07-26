#!/usr/bin/env bash
# go-mk-bootstrap.sh: provision every go-makefile asset into .make.
#
# bootstrap.mk delegates here so fetch policy lives in a fetched file rather
# than in the copy each consumer commits. A policy change therefore ships to
# every consumer on its next run, with no consumer pull request.
#
# Provisioning is staged: one tarball extracts into a temp directory, every
# required asset is verified there, and only then are the files under .make
# replaced. Nothing is deleted before its replacement exists, so a failed or
# partial download leaves the previous assets exactly as they were.

set -euo pipefail

GO_MK_API_REPO="${GO_MK_API_REPO:-agoodkind/go-makefile}"
GO_MK_API_REF="${GO_MK_API_REF:-main}"
# Internal override, in the same category as GO_MK_API_REPO and GO_MK_API_REF.
# Tests point it at a local server; consumers never set it.
GO_MK_CODELOAD_BASE="${GO_MK_CODELOAD_BASE:-https://codeload.github.com}"
GO_MK_DEV_DIR="${GO_MK_DEV_DIR:-}"
GO_MK_MODULES="${GO_MK_MODULES:-}"

MAKE_DIR=".make"
FETCH_MAX_TIME=30

required_assets() {
    printf '%s\n' "go.mk"
    printf '%s\n' "golangci.yml"
    printf '%s\n' "notices.txt"
    printf '%s\n' "scripts/go-mk-fetch-one.sh"
    printf '%s\n' "scripts/go-mk-bin.sh"
    printf '%s\n' "scripts/go-mk-sync.sh"
    local module_name
    for module_name in ${GO_MK_MODULES}; do
        printf '%s\n' "${module_name}"
    done
}

assets_complete() {
    local base_dir="$1"
    local asset_name
    while IFS= read -r asset_name; do
        if [[ ! -s "${base_dir}/${asset_name}" ]]; then
            return 1
        fi
    done < <(required_assets)
    return 0
}

# install_from_stage copies each verified asset out of the staging tree into
# .make. It runs only after assets_complete succeeded against the stage, so a
# copy here always overwrites a file with known-good content.
install_from_stage() {
    local stage_dir="$1"
    local asset_name
    local target_path
    while IFS= read -r asset_name; do
        target_path="${MAKE_DIR}/${asset_name}"
        mkdir -p "$(dirname "${target_path}")"
        cp "${stage_dir}/${asset_name}" "${target_path}"
        case "${target_path}" in
            *.sh) chmod +x "${target_path}" ;;
        esac
    done < <(required_assets)
}

install_from_dev_dir() {
    local asset_name
    local target_path
    while IFS= read -r asset_name; do
        if [[ ! -f "${GO_MK_DEV_DIR}/${asset_name}" ]]; then
            continue
        fi
        target_path="${MAKE_DIR}/${asset_name}"
        mkdir -p "$(dirname "${target_path}")"
        cp "${GO_MK_DEV_DIR}/${asset_name}" "${target_path}"
        case "${target_path}" in
            *.sh) chmod +x "${target_path}" ;;
        esac
    done < <(required_assets)
}

# provision downloads, extracts into a stage, verifies, then installs.
#
# The staging work runs in a subshell rather than the function body itself. A
# RETURN trap set inside a shell function is not scoped to that function: it
# stays armed and fires again on the next function return anywhere in the
# script, by which point stage_root is out of scope and set -u aborts the
# whole run. A subshell's own EXIT trap only ever fires once, when that
# subshell exits, so cleanup cannot leak into unrelated control flow.
provision() {
    local stage_root
    local stage_dir
    local status_code
    local subshell_status

    stage_root=$(mktemp -d "${TMPDIR:-/tmp}/go-mk-stage.XXXXXXXX") || return 1

    (
        trap 'rm -rf "${stage_root}"' EXIT

        if ! status_code=$(curl -sS --connect-timeout 5 --max-time "${FETCH_MAX_TIME}" \
            --retry 3 --retry-delay 2 \
            -o "${stage_root}/snapshot.tar.gz" -w '%{http_code}' \
            "${GO_MK_CODELOAD_BASE}/${GO_MK_API_REPO}/tar.gz/${GO_MK_API_REF}" 2>/dev/null); then
            exit 1
        fi
        if [[ "${status_code}" != "200" ]]; then
            exit 1
        fi

        stage_dir="${stage_root}/tree"
        mkdir -p "${stage_dir}"
        if ! tar -xzf "${stage_root}/snapshot.tar.gz" -C "${stage_dir}" --strip-components 1 2>/dev/null; then
            exit 1
        fi
        if ! assets_complete "${stage_dir}"; then
            exit 1
        fi
        install_from_stage "${stage_dir}"
    )
    subshell_status=$?
    return "${subshell_status}"
}

main() {
    mkdir -p "${MAKE_DIR}"

    if [[ -n "${GO_MK_DEV_DIR}" ]]; then
        install_from_dev_dir
        return 0
    fi

    if [[ "${GO_MK_SKIP_FETCH:-}" == "1" ]]; then
        if assets_complete "${MAKE_DIR}"; then
            return 0
        fi
        printf '%s\n' "error: GO_MK_SKIP_FETCH=1 but .make is missing a required asset" >&2
        return 1
    fi

    if provision; then
        return 0
    fi

    printf '%s\n' "error: could not provision go-makefile assets. Set GO_MK_DEV_DIR, or check network access to ${GO_MK_CODELOAD_BASE}" >&2
    return 1
}

main "$@"
