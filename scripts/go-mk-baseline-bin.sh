#!/usr/bin/env bash
set -eo pipefail

# Resolve the go-mk-baseline binary on demand. Modeled on
# scripts/go-mk-staticcheck-extra.sh. Resolution order: an explicit
# GO_MK_BASELINE_BIN; a dev build from GO_MK_BASELINE_BUILD_REPO with find -newer
# staleness; otherwise `go install GO_MK_BASELINE_INSTALL`. The default install
# spec tracks the main branch tip (@main), so consumers always resolve the
# current engine with no version pin, and the @main arm reinstalls every run.

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${SCRIPT_DIR}/go-mk-common.sh"

baseline_output_path() {
    printf "%s/.make/go-mk-baseline\n" "${GO_MK_ROOT:-${PWD}}"
}

baseline_missing_flags() {
    local candidate_path
    local available_file

    candidate_path="$1"
    go_mk_setup_temp_dir
    available_file="${GO_MK_TEMP_DIR}/go-mk-baseline-flags.out"
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
    repo_path="${GO_MK_BASELINE_BUILD_REPO:-}"
    package_path="${GO_MK_BASELINE_BUILD_PKG:-./cmd/go-mk-baseline}"
    original_dir="${PWD}"
    mkdir -p "$(dirname "${output_path}")"
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

    install_spec="${GO_MK_BASELINE_INSTALL:-goodkind.io/go-makefile/cmd/go-mk-baseline@main}"
    binary_name=$(basename "${install_spec%%@*}")
    go_bin=$(go env GOPATH)/bin
    installed_path="${go_bin}/${binary_name}"
    output_path=$(baseline_output_path)
    go_mk_setup_temp_dir
    error_file="${GO_MK_TEMP_DIR}/go-mk-baseline-install.err"

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

    configured_bin="${GO_MK_BASELINE_BIN:-}"
    repo_path="${GO_MK_BASELINE_BUILD_REPO:-}"
    package_path="${GO_MK_BASELINE_BUILD_PKG:-}"
    install_spec="${GO_MK_BASELINE_INSTALL:-goodkind.io/go-makefile/cmd/go-mk-baseline@main}"
    output_path=$(baseline_output_path)

    if [[ -n "${configured_bin}" ]]; then
        if [[ ! -x "${configured_bin}" ]]; then
            printf "go-mk-baseline: %s not executable\n" "${configured_bin}"
            return 1
        fi
        if baseline_missing_flags "${configured_bin}"; then
            printf "go-mk-baseline: %s does not support the required capabilities\n" "${configured_bin}"
            return 1
        fi
        return 0
    fi

    if [[ -n "${repo_path}" ]]; then
        if [[ ! -d "${repo_path}" ]]; then
            printf "go-mk-baseline: build repo %s not present; skipping\n" "${repo_path}"
            return 0
        fi
        newest_source=""
        if [[ -x "${output_path}" ]]; then
            go_mk_setup_temp_dir
            find_error="${GO_MK_TEMP_DIR}/go-mk-baseline-find.err"
            newest_source=$(find "${repo_path}/cmd/go-mk-baseline" "${repo_path}/internal/baseline" -name "*.go" -newer "${output_path}" 2>"${find_error}" | head -1 || true)
        fi
        if [[ ! -x "${output_path}" || -n "${newest_source}" ]] || baseline_missing_flags "${output_path}"; then
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

    configured_bin="${GO_MK_BASELINE_BIN:-}"
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
        printf "go-mk-baseline-bin: unknown command %s\n" "${command_name}"
        exit 2
        ;;
esac
