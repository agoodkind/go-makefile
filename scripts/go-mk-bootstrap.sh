#!/usr/bin/env bash
# go-mk-bootstrap.sh: obtain the go-mk engine, then exec provision.
#
# bootstrap.mk delegates here so fetch policy lives in a fetched file rather
# than in the copy each consumer commits. This script only obtains go-mk.
# Provisioning (codeload ETag/304, stage-then-rename, reuse) is go-mk provision.

set -euo pipefail

GO_MK_API_REPO="${GO_MK_API_REPO:-agoodkind/go-makefile}"
GO_MK_API_REF="${GO_MK_API_REF:-main}"
GO_MK_CODELOAD_BASE="${GO_MK_CODELOAD_BASE:-https://codeload.github.com}"
GO_MK_DEV_DIR="${GO_MK_DEV_DIR:-}"
GO_MK_MODULES="${GO_MK_MODULES:-}"
MAKE_DIR=".make"
GO_MK_OUTPUT="${MAKE_DIR}/go-mk"

obtain_go_mk() {
    mkdir -p "${MAKE_DIR}"
    # go build -C treats a relative -o as relative to the changed directory, so
    # the consumer tree path must be absolute.
    local output_path
    output_path="$(pwd)/${GO_MK_OUTPUT}"
    if [[ -n "${GO_MK_DEV_DIR}" ]] && command -v go >/dev/null 2>&1 \
        && [[ -d "${GO_MK_DEV_DIR}/cmd/go-mk" ]]; then
        if go build -C "${GO_MK_DEV_DIR}" -o "${output_path}" ./cmd/go-mk; then
            printf '%s\n' "${output_path}"
            return 0
        fi
    fi
    if [[ -x "${output_path}" ]]; then
        printf '%s\n' "${output_path}"
        return 0
    fi
    if ! command -v go >/dev/null 2>&1; then
        printf '%s\n' "error: could not obtain go-mk; go is not on PATH" >&2
        return 1
    fi
    local install_ref="${GO_MK_API_REF}"
    if [[ "${install_ref}" == "main" ]]; then
        install_ref="main"
    fi
    local install_spec="goodkind.io/go-makefile/cmd/go-mk@${install_ref}"
    local go_bin
    go_bin=$(go env GOPATH)/bin
    if ! env GOPROXY=direct GOPRIVATE=goodkind.io/go-makefile GOBIN="${go_bin}" \
        go install "${install_spec}"; then
        printf '%s\n' "error: could not go install ${install_spec}" >&2
        return 1
    fi
    ln -sf "${go_bin}/go-mk" "${output_path}"
    printf '%s\n' "${output_path}"
}

go_mk_bin=$(obtain_go_mk) || exit 1
exec env \
    GO_MK_API_REPO="${GO_MK_API_REPO}" \
    GO_MK_API_REF="${GO_MK_API_REF}" \
    GO_MK_MODULES="${GO_MK_MODULES}" \
    GO_MK_CODELOAD_BASE="${GO_MK_CODELOAD_BASE}" \
    GO_MK_DEV_DIR="${GO_MK_DEV_DIR}" \
    _GO_MK_PROVISIONED="${_GO_MK_PROVISIONED:-}" \
    "${go_mk_bin}" provision
