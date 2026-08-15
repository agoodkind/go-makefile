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
        if go build -C "${GO_MK_BUILD_REPO}" -o "${OUTPUT}" "${GO_MK_BUILD_PKG:-./cmd/go-mk}"; then
            printf '%s\n' "${OUTPUT}"
            return 0
        fi
    fi
    if [[ -x "${OUTPUT}" ]] && "${OUTPUT}" -flags 2>/dev/null | grep -q "Name: resolve-bin"; then
        printf '%s\n' "${OUTPUT}"
        return 0
    fi
    if ! command -v go >/dev/null 2>&1; then
        printf 'go-mk-bin: go is not on PATH\n' >&2
        return 1
    fi
    local install_spec="${GO_MK_INSTALL:-goodkind.io/go-makefile/cmd/go-mk@main}"
    local go_bin
    go_bin=$(go env GOPATH)/bin
    env GOPROXY=direct GOPRIVATE=goodkind.io/go-makefile GOBIN="${go_bin}" go install "${install_spec}"
    ln -sf "${go_bin}/go-mk" "${OUTPUT}"
    printf '%s\n' "${OUTPUT}"
}

go_mk_bin=$(obtain_go_mk) || exit 1
exec "${go_mk_bin}" resolve-bin "${1:-bin}"
