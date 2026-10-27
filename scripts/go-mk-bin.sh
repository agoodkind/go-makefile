#!/usr/bin/env bash
set -euo pipefail

# Thin wrapper for an old go.mk that still calls this script. Obtain a go-mk
# that understands resolve-bin, then exec it. Do not exec inside a subshell:
# that leaves this process running an older engine afterward.

OUTPUT="${_GO_MK_ROOT:-${GO_MK_ROOT:-$(pwd)}}/.make/go-mk"
if [[ "${OUTPUT}" != /* ]]; then
    OUTPUT="$(pwd)/${OUTPUT}"
fi

# engine_provides reports whether a go-mk binary advertises one command. The
# output is captured before it is matched: piping into `grep -q` under pipefail
# fails the pipeline when grep exits early and the binary dies of SIGPIPE, so a
# binary that does provide the command reads as one that does not.
engine_provides() {
    local binary_path="$1"
    local command_name="$2"
    local advertised

    advertised=$("${binary_path}" -flags 2>/dev/null) || return 1
    case "${advertised}" in
        *"Name: ${command_name}"*) return 0 ;;
        *) return 1 ;;
    esac
}

obtain_go_mk() {
    local required_command="$1"
    mkdir -p "$(dirname "${OUTPUT}")"
    if [[ -n "${GO_MK_BUILD_REPO:-}" ]]; then
        local tmp_path
        tmp_path=$(mktemp "${OUTPUT}.XXXXXX") || return 1
        if go build -C "${GO_MK_BUILD_REPO}" -o "${tmp_path}" "${GO_MK_BUILD_PKG:-./cmd/go-mk}" \
            && mv "${tmp_path}" "${OUTPUT}"; then
            printf '%s\n' "${OUTPUT}"
            return 0
        fi
        rm -f "${tmp_path}"
        printf 'go-mk-bin: dev build failed; correct GO_MK_BUILD_REPO before running targets\n' >&2
        return 1
    fi
    # A symlink here is an engine some earlier version shared with every other
    # consumer on the machine. It is replaced rather than reused, whatever it
    # currently points at, so this consumer ends up owning its own binary.
    if [[ ! -L "${OUTPUT}" && -f "${OUTPUT}" && -x "${OUTPUT}" ]] \
        && engine_provides "${OUTPUT}" "${required_command}"; then
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
    local -a install_environment
    install_environment=("GOBIN=${staging}")
    if [[ "${required_command}" == "validate-make" ]] \
        && [[ "${install_spec}" == "goodkind.io/go-makefile/cmd/go-mk@main" ]] \
        && [[ ! -s "$(dirname "${OUTPUT}")/.go-mk-commit" ]]; then
        install_environment+=("GOPROXY=direct")
    fi
    if ! env "${install_environment[@]}" \
        go install "${install_spec}"; then
        rm -rf "${staging}"
        printf 'go-mk-bin: could not go install %s\n' "${install_spec}" >&2
        return 1
    fi
    if ! engine_provides "${staging}/go-mk" "${required_command}"; then
        rm -rf "${staging}"
        printf 'go-mk-bin: the installed engine does not provide %s; update GO_MK_INSTALL to a revision that provides this command\n' "${required_command}" >&2
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

run_make_validation() {
    local validation_status=0
    local go_mk_bin="${GO_MK_BIN:-}"
    if [[ -z "${go_mk_bin}" ]]; then
        if go_mk_bin=$(obtain_go_mk validate-make); then
            :
        else
            validation_status=$?
        fi
    fi
    if [[ "${validation_status}" -eq 0 ]]; then
        if "${go_mk_bin}" validate-make "$@"; then
            :
        else
            validation_status=$?
        fi
    fi
    printf '\n__GO_MK_VALIDATE_EXIT_%s\n' "${validation_status}"
    return "${validation_status}"
}

if [[ "${1:-bin}" == "validate-make" ]]; then
    shift
    run_make_validation "$@"
else
    go_mk_bin=$(obtain_go_mk resolve-bin) || exit 1
    exec "${go_mk_bin}" resolve-bin "${1:-bin}"
fi
