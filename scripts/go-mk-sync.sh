#!/usr/bin/env bash
set -euo pipefail

# Thin wrapper: provision owns the asset tree. An old go.mk still calls this
# script for update-go-mk and smoke-fetch.

OUTPUT="${_GO_MK_ROOT:-${GO_MK_ROOT:-$(pwd)}}/.make/go-mk"
if [[ "${OUTPUT}" != /* ]]; then
    OUTPUT="$(pwd)/${OUTPUT}"
fi

obtain_go_mk() {
    if [[ -x "${OUTPUT}" ]]; then
        printf '%s\n' "${OUTPUT}"
        return 0
    fi
    if [[ -n "${GO_MK_BUILD_REPO:-${GO_MK_DEV_DIR:-}}" ]]; then
        local repo="${GO_MK_BUILD_REPO:-${GO_MK_DEV_DIR}}"
        mkdir -p "$(dirname "${OUTPUT}")"
        local tmp_path
        tmp_path=$(mktemp "${OUTPUT}.XXXXXX") || return 1
        if ! go build -C "${repo}" -o "${tmp_path}" "${GO_MK_BUILD_PKG:-./cmd/go-mk}"; then
            rm -f "${tmp_path}"
            return 1
        fi
        mv "${tmp_path}" "${OUTPUT}"
        printf '%s\n' "${OUTPUT}"
        return 0
    fi
    printf 'go-mk-sync: go-mk engine is missing; run a parse first\n' >&2
    return 1
}

command_name="${1:-}"
go_mk_bin=$(obtain_go_mk) || exit 1
case "${command_name}" in
    update)
        exec "${go_mk_bin}" provision
        ;;
    smoke-fetch)
        kept=$(mktemp "${TMPDIR:-/tmp}/go-mk-smoke.XXXXXXXX") || exit 1
        cp "${go_mk_bin}" "${kept}"
        chmod +x "${kept}"
        rm -rf .make
        mkdir -p .make
        cp "${kept}" "${OUTPUT}"
        chmod +x "${OUTPUT}"
        rm -f "${kept}"
        exec "${OUTPUT}" provision
        ;;
    *)
        printf "go-mk-sync: unknown command %s\n" "${command_name}"
        exit 2
        ;;
esac
