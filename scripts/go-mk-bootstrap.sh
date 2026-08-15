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
        local tmp_path
        tmp_path=$(mktemp "${output_path}.XXXXXX") || return 1
        if go build -C "${GO_MK_DEV_DIR}" -o "${tmp_path}" ./cmd/go-mk \
            && mv "${tmp_path}" "${output_path}"; then
            printf '%s\n' "${output_path}"
            return 0
        fi
        rm -f "${tmp_path}"
    fi
    # An existing binary is reused only when it provides the command this script
    # is about to exec. Testing that it merely exists would reuse an engine from
    # before provision was added and then fail on the exec, which is exactly what
    # a consumer meets on the run after this script first replaces its
    # predecessor.
    # A symlink here is an engine some earlier version shared with every other
    # consumer on the machine. It is replaced rather than reused, whatever it
    # currently points at, so this consumer ends up owning its own binary.
    if [[ ! -L "${output_path}" && -f "${output_path}" && -x "${output_path}" ]] \
        && "${output_path}" -flags 2>/dev/null | grep -q "Name: provision"; then
        printf '%s\n' "${output_path}"
        return 0
    fi
    if ! command -v go >/dev/null 2>&1; then
        printf '%s\n' "error: could not obtain go-mk; go is not on PATH" >&2
        return 1
    fi
    install_go_mk "${output_path}" "goodkind.io/go-makefile/cmd/go-mk@${GO_MK_API_REF}"
}

# install_go_mk installs one engine into the consumer's own .make, never into a
# shared GOPATH bin. GOBIN points at a temporary directory inside .make, so the
# rename into place stays on one filesystem and is atomic.
#
# A machine-wide binary cannot serve two consumers pinned to different refs:
# each build would rewrite it for the other, and a symlink from .make/go-mk
# would then resolve to the wrong engine. Measured before this change, two
# clones of one repository on two refs alternated, and the fourth build failed
# with unknown command "provision" because the third had rewritten the shared
# binary.
install_go_mk() {
    local output_path="$1"
    local install_spec="$2"
    local staging

    staging=$(mktemp -d "$(dirname "${output_path}")/.go-mk-install.XXXXXX") || return 1
    if ! env GOPROXY=direct GOPRIVATE=goodkind.io/go-makefile GOBIN="${staging}" \
        go install "${install_spec}"; then
        rm -rf "${staging}"
        printf '%s\n' "error: could not go install ${install_spec}" >&2
        return 1
    fi
    if ! mv "${staging}/go-mk" "${output_path}"; then
        rm -rf "${staging}"
        printf '%s\n' "error: could not install go-mk into ${output_path}" >&2
        return 1
    fi
    rm -rf "${staging}"
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
