#!/usr/bin/env bash
set -eo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${SCRIPT_DIR}/go-mk-common.sh"

staticcheck_output_path() {
    printf "%s/.make/staticcheck-extra\n" "${GO_MK_ROOT:-${PWD}}"
}

staticcheck_missing_flags() {
    local candidate_path
    local flags_text
    local available_file
    local flag_word
    local flag_name

    candidate_path="$1"
    flags_text="${STATICCHECK_EXTRA_FLAGS:-}"
    go_mk_setup_temp_dir
    available_file="${GO_MK_TEMP_DIR}/staticcheck-flags.out"
    go_mk_run_capture "${available_file}" "${candidate_path}" -flags

    for flag_word in ${flags_text}; do
        flag_name="${flag_word#-}"
        if ! grep -q "Name.*${flag_name}" "${available_file}"; then
            return 0
        fi
    done
    return 1
}

staticcheck_build_from_repo() {
    local output_path
    local repo_path
    local package_path
    local original_dir

    output_path=$(staticcheck_output_path)
    repo_path="${STATICCHECK_EXTRA_BUILD_REPO:-}"
    package_path="${STATICCHECK_EXTRA_BUILD_PKG:-./cmd/staticcheck-extra}"
    original_dir="${PWD}"
    mkdir -p "$(dirname "${output_path}")"
    cd "${repo_path}"
    go_mk_run_lint_cpu go build -o "${output_path}" "${package_path}"
    cd "${original_dir}"
}

staticcheck_install_binary() {
    local install_spec
    local binary_name
    local go_bin
    local installed_path
    local output_path
    local error_file

    install_spec="${STATICCHECK_EXTRA_INSTALL:-goodkind.io/go-makefile/staticcheck/cmd/staticcheck-extra@latest}"
    binary_name=$(basename "${install_spec%%@*}")
    go_bin=$(go env GOPATH)/bin
    installed_path="${go_bin}/${binary_name}"
    output_path=$(staticcheck_output_path)
    go_mk_setup_temp_dir
    error_file="${GO_MK_TEMP_DIR}/staticcheck-install.err"

    if ! go_mk_run_lint_cpu env \
        GOPROXY=direct \
        GONOSUMDB=goodkind.io/go-makefile,goodkind.io/go-makefile/staticcheck \
        GOBIN="${go_bin}" \
        go install "${install_spec}" 2>"${error_file}"; then
        cat "${error_file}"
        return 1
    fi
    mkdir -p "$(dirname "${output_path}")"
    ln -sf "${installed_path}" "${output_path}"
}

staticcheck_resolve_bin() {
    local configured_bin
    local repo_path
    local package_path
    local install_spec
    local output_path
    local go_bin
    local binary_name
    local installed_path
    local newest_source
    local find_error

    configured_bin="${STATICCHECK_EXTRA_BIN:-}"
    repo_path="${STATICCHECK_EXTRA_BUILD_REPO:-}"
    package_path="${STATICCHECK_EXTRA_BUILD_PKG:-}"
    install_spec="${STATICCHECK_EXTRA_INSTALL:-goodkind.io/go-makefile/staticcheck/cmd/staticcheck-extra@latest}"
    output_path=$(staticcheck_output_path)

    if [[ -n "${configured_bin}" ]]; then
        if [[ ! -x "${configured_bin}" ]]; then
            printf "staticcheck-extra: %s not executable\n" "${configured_bin}"
            return 1
        fi
        if staticcheck_missing_flags "${configured_bin}"; then
            printf "staticcheck-extra: %s does not support requested flags\n" "${configured_bin}"
            return 1
        fi
        return 0
    fi

    if [[ -n "${repo_path}" ]]; then
        if [[ ! -d "${repo_path}" ]]; then
            printf "staticcheck-extra: build repo %s not present; skipping\n" "${repo_path}"
            return 0
        fi
        if [[ -z "${package_path}" ]]; then
            printf "staticcheck-extra: STATICCHECK_EXTRA_BUILD_PKG not set\n"
            return 1
        fi
        newest_source=""
        if [[ -x "${output_path}" ]]; then
            go_mk_setup_temp_dir
            find_error="${GO_MK_TEMP_DIR}/staticcheck-find.err"
            newest_source=$(find "${repo_path}" -name "*.go" -newer "${output_path}" 2>"${find_error}" | head -1 || true)
        fi
        if [[ ! -x "${output_path}" || -n "${newest_source}" ]] || staticcheck_missing_flags "${output_path}"; then
            staticcheck_build_from_repo
        fi
        return 0
    fi

    if [[ -z "${install_spec}" ]]; then
        return 0
    fi

    binary_name=$(basename "${install_spec%%@*}")
    go_bin=$(go env GOPATH)/bin
    installed_path="${go_bin}/${binary_name}"
    case "${install_spec}" in
        *@latest)
            staticcheck_install_binary
            ;;
        *)
            if [[ ! -x "${installed_path}" ]] || staticcheck_missing_flags "${installed_path}"; then
                staticcheck_install_binary
            else
                mkdir -p "$(dirname "${output_path}")"
                ln -sf "${installed_path}" "${output_path}"
            fi
            ;;
    esac
}

staticcheck_selected_bin() {
    local configured_bin
    local output_path

    configured_bin="${STATICCHECK_EXTRA_BIN:-}"
    output_path=$(staticcheck_output_path)
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

staticcheck_capture_findings() {
    local raw_output
    local findings_output
    local selected_bin
    local exclude_pattern
    local flags_text
    local flag_args
    local target_args
    local normalized_output
    local filtered_output

    raw_output="$1"
    findings_output="$2"
    selected_bin=$(staticcheck_selected_bin)
    if [[ -z "${selected_bin}" ]]; then
        printf "staticcheck-extra: not configured (skipped)\n"
        : > "${findings_output}"
        return 0
    fi
    if [[ ! -x "${selected_bin}" ]]; then
        printf "staticcheck-extra: binary %s not executable; skipping\n" "${selected_bin}"
        : > "${findings_output}"
        return 0
    fi

    flags_text="${STATICCHECK_EXTRA_FLAGS:-}"
    go_mk_split_words "${flags_text}"
    flag_args=("${GO_MK_WORDS[@]}")
    go_mk_split_words "${STATICCHECK_EXTRA_TARGETS:-./...}"
    target_args=("${GO_MK_WORDS[@]}")
    exclude_pattern=$(go_mk_exclude_pattern "${STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${STATICCHECK_EXTRA_EXCLUDE_PATHS:-}")

    go_mk_setup_temp_dir
    normalized_output="${GO_MK_TEMP_DIR}/staticcheck-normalized.out"
    filtered_output="${GO_MK_TEMP_DIR}/staticcheck-filtered.out"
    go_mk_run_lint_capture "${raw_output}" "${selected_bin}" "${flag_args[@]}" "${target_args[@]}"
    go_mk_normalize_file "${raw_output}" "${normalized_output}"
    go_mk_filter_file "${normalized_output}" "${filtered_output}" "${exclude_pattern}"
    sort -u "${filtered_output}" > "${findings_output}"
}

staticcheck_run_gate() {
    local raw_output
    local findings_output
    local exclude_pattern
    local scope_pattern
    local suppress_fixed_count

    mkdir -p .make
    raw_output=".make/staticcheck-extra.raw.out"
    findings_output=".make/staticcheck-extra.out"
    exclude_pattern=$(go_mk_exclude_pattern "${STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${STATICCHECK_EXTRA_EXCLUDE_PATHS:-}")
    scope_pattern=$(go_mk_staticcheck_baseline_scope_pattern)
    suppress_fixed_count=$(go_mk_staticcheck_suppress_fixed_count)
    staticcheck_capture_findings "${raw_output}" "${findings_output}"
    go_mk_run_baseline_diff_gate \
        "staticcheck-extra" \
        "${findings_output}" \
        "${STATICCHECK_EXTRA_BASELINE:-.staticcheck-extra-baseline.txt}" \
        "staticcheck-extra" \
        "Fix the new findings before this gate will pass." \
        "${exclude_pattern}" \
        "${scope_pattern}" \
        "${suppress_fixed_count}"
}

command_name="${1:-}"
case "${command_name}" in
    bin)
        staticcheck_resolve_bin
        ;;
    run)
        staticcheck_run_gate
        ;;
    capture)
        mkdir -p .make
        staticcheck_capture_findings "${2:-.make/staticcheck-extra.raw.out}" "${3:-.make/staticcheck-extra.out}"
        ;;
    *)
        printf "go-mk-staticcheck-extra: unknown command %s\n" "${command_name}"
        exit 2
        ;;
esac
