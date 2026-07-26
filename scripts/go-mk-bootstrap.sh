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

# assets_complete requires each required asset to be a regular file with
# nonzero size. -s alone is true for a directory too (its apparent size is
# nonzero), so -f is required alongside it to reject a directory standing in
# for an asset.
assets_complete() {
    local base_dir="$1"
    local asset_name
    while IFS= read -r asset_name; do
        if [[ ! -f "${base_dir}/${asset_name}" || ! -s "${base_dir}/${asset_name}" ]]; then
            return 1
        fi
    done < <(required_assets)
    return 0
}

# install_one_asset copies a single verified asset from source_dir into .make,
# checking every step explicitly rather than relying on set -e. provision()
# runs this from a subshell used as an if-condition, and bash disables
# errexit for the entire duration of a command (including anything a
# subshell it spawns does) while that command's exit status is being tested
# by if/while/&&/||. A failed mkdir, cp, or chmod partway through the loop
# would otherwise be silently ignored, leaving install_from_stage's overall
# exit status determined only by whichever command the last loop iteration
# happened to run.
install_one_asset() {
    local source_path="$1"
    local target_path="$2"
    local target_dir

    target_dir="$(dirname "${target_path}")"
    if ! mkdir -p "${target_dir}"; then
        printf 'error: could not create %s\n' "${target_dir}" >&2
        return 1
    fi
    if ! cp "${source_path}" "${target_path}"; then
        printf 'error: could not install %s into %s\n' "${source_path}" "${target_path}" >&2
        return 1
    fi
    case "${target_path}" in
        *.sh)
            if ! chmod +x "${target_path}"; then
                printf 'error: could not mark %s executable\n' "${target_path}" >&2
                return 1
            fi
            ;;
    esac
    return 0
}

# install_from_stage copies each verified asset out of the staging tree into
# .make. It runs only after assets_complete succeeded against the stage, so a
# copy here always overwrites a file with known-good content. It stops and
# fails on the first asset that does not install cleanly.
install_from_stage() {
    local stage_dir="$1"
    local asset_name
    while IFS= read -r asset_name; do
        if ! install_one_asset "${stage_dir}/${asset_name}" "${MAKE_DIR}/${asset_name}"; then
            return 1
        fi
    done < <(required_assets)
    return 0
}

# install_from_dev_dir copies whatever required assets GO_MK_DEV_DIR provides.
# It intentionally skips an asset the dev dir does not carry, so a developer
# tree only needs to override the files under active work; the caller is
# responsible for checking assets_complete against MAKE_DIR afterward, since
# a dev dir that skips an asset .make does not already have would otherwise
# leave .make incomplete.
install_from_dev_dir() {
    local asset_name
    while IFS= read -r asset_name; do
        if [[ ! -f "${GO_MK_DEV_DIR}/${asset_name}" ]]; then
            continue
        fi
        if ! install_one_asset "${GO_MK_DEV_DIR}/${asset_name}" "${MAKE_DIR}/${asset_name}"; then
            return 1
        fi
    done < <(required_assets)
    return 0
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
    local curl_exit
    local tar_exit
    local subshell_status

    stage_root=$(mktemp -d "${TMPDIR:-/tmp}/go-mk-stage.XXXXXXXX") || return 1

    (
        trap 'rm -rf "${stage_root}"' EXIT

        # Each probe's own stderr and exit code are captured and surfaced
        # rather than discarded, so a corrupt tarball, an HTTP error, and an
        # unreachable host are distinguishable instead of collapsing into one
        # generic message.
        curl -sS --connect-timeout 5 --max-time "${FETCH_MAX_TIME}" \
            --retry 3 --retry-delay 2 \
            -o "${stage_root}/snapshot.tar.gz" -w '%{http_code}' \
            "${GO_MK_CODELOAD_BASE}/${GO_MK_API_REPO}/tar.gz/${GO_MK_API_REF}" \
            >"${stage_root}/status_code" 2>"${stage_root}/curl.stderr"
        curl_exit=$?
        if [[ "${curl_exit}" -ne 0 ]]; then
            printf 'error: curl exited %s fetching the tarball: %s\n' \
                "${curl_exit}" "$(cat "${stage_root}/curl.stderr")" >&2
            exit 1
        fi
        status_code=$(cat "${stage_root}/status_code")
        if [[ "${status_code}" != "200" ]]; then
            printf 'error: tarball fetch returned HTTP %s from %s\n' \
                "${status_code}" "${GO_MK_CODELOAD_BASE}" >&2
            exit 1
        fi

        stage_dir="${stage_root}/tree"
        mkdir -p "${stage_dir}"
        tar -xzf "${stage_root}/snapshot.tar.gz" -C "${stage_dir}" --strip-components 1 \
            2>"${stage_root}/tar.stderr"
        tar_exit=$?
        if [[ "${tar_exit}" -ne 0 ]]; then
            printf 'error: tar extract exited %s: %s\n' \
                "${tar_exit}" "$(cat "${stage_root}/tar.stderr")" >&2
            exit 1
        fi
        if ! assets_complete "${stage_dir}"; then
            printf 'error: staged tree is missing a required asset\n' >&2
            exit 1
        fi
        if ! install_from_stage "${stage_dir}"; then
            exit 1
        fi
        # Re-verify what actually landed in .make rather than trusting that
        # every install step reported success, as a second, independent check
        # alongside install_one_asset's own per-step error handling.
        if ! assets_complete "${MAKE_DIR}"; then
            printf 'error: .make is incomplete after install\n' >&2
            exit 1
        fi
    )
    subshell_status=$?
    return "${subshell_status}"
}

main() {
    mkdir -p "${MAKE_DIR}"

    if [[ -n "${GO_MK_DEV_DIR}" ]]; then
        if ! install_from_dev_dir; then
            printf '%s\n' "error: could not install from GO_MK_DEV_DIR=${GO_MK_DEV_DIR}" >&2
            return 1
        fi
        if ! assets_complete "${MAKE_DIR}"; then
            printf '%s\n' "error: GO_MK_DEV_DIR=${GO_MK_DEV_DIR} does not provide every required asset" >&2
            return 1
        fi
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
