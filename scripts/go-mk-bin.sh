#!/usr/bin/env bash
set -eo pipefail

# Resolve the go-mk binary on demand. Modeled on
# scripts/go-mk-staticcheck-extra.sh. Resolution order: an explicit
# GO_MK_BIN; a dev build from GO_MK_BUILD_REPO with find -newer
# staleness; otherwise `go install GO_MK_INSTALL`. The default install
# spec tracks the main branch tip (@main), so consumers always resolve the
# current engine with no version pin, and the @main arm reinstalls every run.

# This bootstrap resolver is self-contained: it inlines the few helpers it needs
# (temp dir, capture, lint-concurrency build env) rather than sourcing the former
# go-mk-common.sh, which has been collapsed into the go-mk binary's internal
# packages. Only the binary build/resolve bootstrap remains as shell.
GO_MK_TEMP_DIR=""
GO_MK_COMMAND_STATUS=0
GO_MK_EFFECTIVE_LINT_CONCURRENCY=""

go_mk_cleanup() {
    if [[ -n "${GO_MK_TEMP_DIR}" && -d "${GO_MK_TEMP_DIR}" ]]; then
        rm -rf "${GO_MK_TEMP_DIR}"
    fi
}

trap go_mk_cleanup EXIT

go_mk_setup_temp_dir() {
    if [[ -z "${GO_MK_TEMP_DIR}" ]]; then
        GO_MK_TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/go-mk.XXXXXX")
    fi
}

go_mk_run_capture() {
    local output_file
    local error_file

    output_file="$1"
    shift
    error_file="${output_file}.err"
    GO_MK_COMMAND_STATUS=0
    "$@" > "${output_file}" 2>"${error_file}" || GO_MK_COMMAND_STATUS=$?
    cat "${error_file}" >> "${output_file}"
    rm -f "${error_file}"
}

go_mk_resolve_lint_concurrency() {
    local requested_concurrency
    local processor_count
    local load_text
    local load_average
    local error_file

    if [[ -n "${GO_MK_EFFECTIVE_LINT_CONCURRENCY}" ]]; then
        return
    fi

    requested_concurrency="${LINT_CONCURRENCY:-auto}"
    if [[ "${requested_concurrency}" != "auto" ]]; then
        GO_MK_EFFECTIVE_LINT_CONCURRENCY="${requested_concurrency}"
        return
    fi

    go_mk_setup_temp_dir
    error_file="${GO_MK_TEMP_DIR}/concurrency.err"
    processor_count=$(getconf _NPROCESSORS_ONLN 2>"${error_file}" || sysctl -n hw.ncpu 2>"${error_file}" || printf "4")
    if [[ -z "${processor_count}" || "${processor_count}" -lt 1 ]]; then
        processor_count=1
    fi

    if [[ "$(uname)" == "Darwin" ]]; then
        load_text=$(sysctl -n vm.loadavg 2>"${error_file}" || printf "")
        load_average=$(printf "%s\n" "${load_text}" | awk "{ print \$2 + 0 }")
    else
        load_text=$(cat /proc/loadavg 2>"${error_file}" || printf "")
        load_average=$(printf "%s\n" "${load_text}" | awk "{ print \$1 + 0; exit }")
    fi
    if [[ -z "${load_average}" ]]; then
        load_average=0
    fi

    GO_MK_EFFECTIVE_LINT_CONCURRENCY=$(
        awk -v processor_count="${processor_count}" -v load_average="${load_average}" '
            BEGIN {
                value = int(processor_count - load_average - 1)
                minimum = processor_count < 2 ? 1 : 2
                if (value < minimum) {
                    value = minimum
                }
                if (value > processor_count) {
                    value = processor_count
                }
                print value
            }
        '
    )
}

go_mk_lint_goflags() {
    local existing_flags
    local output_flags
    local flag_word

    existing_flags="${GOFLAGS:-}"
    output_flags=""
    for flag_word in ${existing_flags}; do
        if [[ "${flag_word}" == -p=* ]]; then
            continue
        fi
        if [[ -z "${output_flags}" ]]; then
            output_flags="${flag_word}"
        else
            output_flags="${output_flags} ${flag_word}"
        fi
    done

    if [[ -z "${output_flags}" ]]; then
        printf -- "-p=%s\n" "${GO_MK_EFFECTIVE_LINT_CONCURRENCY}"
    else
        printf "%s -p=%s\n" "${output_flags}" "${GO_MK_EFFECTIVE_LINT_CONCURRENCY}"
    fi
}

go_mk_run_lint_cpu() {
    go_mk_resolve_lint_concurrency
    if [[ "${GO_MK_EFFECTIVE_LINT_CONCURRENCY}" == "0" ]]; then
        "$@"
        return
    fi
    env GOMAXPROCS="${GO_MK_EFFECTIVE_LINT_CONCURRENCY}" GOFLAGS="$(go_mk_lint_goflags)" "$@"
}

baseline_output_path() {
    printf "%s/.make/go-mk\n" "${GO_MK_ROOT:-${PWD}}"
}

baseline_missing_flags() {
    local candidate_path
    local available_file

    candidate_path="$1"
    go_mk_setup_temp_dir
    available_file="${GO_MK_TEMP_DIR}/go-mk-flags.out"
    go_mk_run_capture "${available_file}" "${candidate_path}" -flags
    if ! grep -q "Name.*write-batch" "${available_file}"; then
        return 0
    fi
    return 1
}

baseline_build_from_repo() {
    local output_path
    local repo_path
    local package_path
    local original_dir

    output_path=$(baseline_output_path)
    repo_path="${GO_MK_BUILD_REPO:-}"
    package_path="${GO_MK_BUILD_PKG:-./cmd/go-mk}"
    original_dir="${PWD}"
    mkdir -p "$(dirname "${output_path}")"
    # Remove any prior output first so the build replaces a stale dev binary or
    # an install symlink, rather than writing through a symlink into the
    # installed binary.
    rm -f "${output_path}"
    cd "${repo_path}"
    go_mk_run_lint_cpu go build -o "${output_path}" "${package_path}"
    cd "${original_dir}"
}

baseline_install_binary() {
    local install_spec
    local binary_name
    local go_bin
    local installed_path
    local output_path
    local error_file

    install_spec="${GO_MK_INSTALL:-goodkind.io/go-makefile/cmd/go-mk@main}"
    binary_name=$(basename "${install_spec%%@*}")
    go_bin=$(go env GOPATH)/bin
    installed_path="${go_bin}/${binary_name}"
    output_path=$(baseline_output_path)
    go_mk_setup_temp_dir
    error_file="${GO_MK_TEMP_DIR}/go-mk-install.err"

    if ! go_mk_run_lint_cpu env \
        GOPROXY=direct \
        GOPRIVATE=goodkind.io/go-makefile \
        GOBIN="${go_bin}" \
        go install "${install_spec}" 2>"${error_file}"; then
        cat "${error_file}"
        return 1
    fi
    mkdir -p "$(dirname "${output_path}")"
    ln -sf "${installed_path}" "${output_path}"
}

baseline_resolve_bin() {
    local configured_bin
    local repo_path
    local package_path
    local install_spec
    local output_path
    local newest_source
    local find_error
    local rebuild_reason

    configured_bin="${GO_MK_BIN:-}"
    repo_path="${GO_MK_BUILD_REPO:-}"
    package_path="${GO_MK_BUILD_PKG:-}"
    install_spec="${GO_MK_INSTALL:-goodkind.io/go-makefile/cmd/go-mk@main}"
    output_path=$(baseline_output_path)

    if [[ -n "${configured_bin}" ]]; then
        if [[ ! -x "${configured_bin}" ]]; then
            printf "go-mk: %s not executable\n" "${configured_bin}"
            return 1
        fi
        if baseline_missing_flags "${configured_bin}"; then
            printf "go-mk: %s does not support the required capabilities\n" "${configured_bin}"
            return 1
        fi
        return 0
    fi

    if [[ -n "${repo_path}" ]]; then
        if [[ ! -d "${repo_path}" ]]; then
            printf "go-mk: build repo %s not present; skipping\n" "${repo_path}"
            return 0
        fi
        rebuild_reason=""
        if [[ ! -x "${output_path}" ]]; then
            rebuild_reason="missing cached binary"
        elif [[ -L "${output_path}" ]]; then
            # A symlink is an installed (@main) binary, not a dev build. In dev
            # mode always replace it with a fresh build from the dev checkout so
            # a switch from the pinned engine back to the dev checkout takes
            # effect even when the installed binary is newer than the source.
            rebuild_reason="cached binary is an install symlink, not a dev build"
        else
            go_mk_setup_temp_dir
            find_error="${GO_MK_TEMP_DIR}/go-mk-find.err"
            # Rebuild when any build input is newer than the cached binary: a
            # .go file under cmd/go-mk or internal, or the module files go.mod
            # and go.sum, so a dependency bump with no .go change still triggers
            # a rebuild.
            newest_source=$(find "${repo_path}/cmd/go-mk" "${repo_path}/internal" "${repo_path}/go.mod" "${repo_path}/go.sum" \
                \( -name "*.go" -o -name "go.mod" -o -name "go.sum" \) -newer "${output_path}" 2>"${find_error}" | head -1 || true)
            if [[ -n "${newest_source}" ]]; then
                rebuild_reason="source newer than cached binary"
            elif baseline_missing_flags "${output_path}"; then
                rebuild_reason="cached binary missing required capabilities"
            fi
        fi
        if [[ -n "${rebuild_reason}" ]]; then
            baseline_build_from_repo
        fi
        return 0
    fi

    if [[ -z "${install_spec}" ]]; then
        return 0
    fi
    # @main and @latest are moving refs: always reinstall so GOPROXY=direct
    # re-resolves the tip. Do not cache by checking the installed path.
    baseline_install_binary
}

baseline_selected_bin() {
    local configured_bin
    local output_path

    configured_bin="${GO_MK_BIN:-}"
    output_path=$(baseline_output_path)
    if [[ -n "${configured_bin}" ]]; then
        printf "%s\n" "${configured_bin}"
        return
    fi
    if [[ -x "${output_path}" ]]; then
        printf "%s\n" "${output_path}"
        return
    fi
    printf "\n"
}

command_name="${1:-bin}"
case "${command_name}" in
    bin)
        baseline_resolve_bin
        ;;
    selected-bin)
        baseline_selected_bin
        ;;
    *)
        printf "go-mk-bin: unknown command %s\n" "${command_name}"
        exit 2
        ;;
esac
