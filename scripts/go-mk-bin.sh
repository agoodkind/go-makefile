#!/usr/bin/env bash
set -euo pipefail

# Thin wrapper for an old go.mk that still calls this script. Obtain a go-mk
# that understands resolve-bin, then exec it. Do not exec inside a subshell:
# that leaves this process running an older engine afterward.

OUTPUT="${_GO_MK_ROOT:-${GO_MK_ROOT:-$(pwd)}}/.make/go-mk"
if [[ "${OUTPUT}" != /* ]]; then
    OUTPUT="$(pwd)/${OUTPUT}"
fi

obtain_go_mk() {
    mkdir -p "$(dirname "${OUTPUT}")"
    if [[ -n "${GO_MK_BUILD_REPO:-}" ]] && [[ -d "${GO_MK_BUILD_REPO}" ]] && command -v go >/dev/null 2>&1; then
        local tmp_path
        tmp_path=$(mktemp "${OUTPUT}.XXXXXX") || return 1
        if go build -C "${GO_MK_BUILD_REPO}" -o "${tmp_path}" "${GO_MK_BUILD_PKG:-./cmd/go-mk}" \
            && mv "${tmp_path}" "${OUTPUT}"; then
            printf '%s\n' "${OUTPUT}"
            return 0
        fi
        rm -f "${tmp_path}"
    fi
    # A symlink here is an engine some earlier version shared with every other
    # consumer on the machine. It is replaced rather than reused, whatever it
    # currently points at, so this consumer ends up owning its own binary.
    if [[ ! -L "${OUTPUT}" && -f "${OUTPUT}" && -x "${OUTPUT}" ]] \
        && "${OUTPUT}" -flags 2>/dev/null | grep -q "Name: resolve-bin"; then
        printf '%s\n' "${OUTPUT}"
        return 0
    fi
    if ! command -v go >/dev/null 2>&1; then
        printf 'go-mk-bin: go is not on PATH\n' >&2
        return 1
    fi
    # The engine is installed into this consumer's own .make, never into a
    # shared GOPATH bin, so two consumers pinned to different refs do not
    # rewrite each other's binary. GOBIN points at a temporary directory beside
    # the output, keeping the rename on one filesystem.
    local install_spec="${GO_MK_INSTALL:-goodkind.io/go-makefile/cmd/go-mk@main}"
    local staging
    staging=$(mktemp -d "$(dirname "${OUTPUT}")/.go-mk-install.XXXXXX") || return 1
    if ! env GOPROXY=direct GOPRIVATE=goodkind.io/go-makefile GOBIN="${staging}" \
        go install "${install_spec}"; then
        rm -rf "${staging}"
        printf 'go-mk-bin: could not go install %s\n' "${install_spec}" >&2
        return 1
    fi
    if ! mv "${staging}/go-mk" "${OUTPUT}"; then
        rm -rf "${staging}"
        printf 'go-mk-bin: could not install go-mk into %s\n' "${OUTPUT}" >&2
        return 1
    fi
    rm -rf "${staging}"
    printf '%s\n' "${OUTPUT}"
}

go_mk_bin=$(obtain_go_mk) || exit 1
exec "${go_mk_bin}" resolve-bin "${1:-bin}"
