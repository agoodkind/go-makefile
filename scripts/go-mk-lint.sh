#!/usr/bin/env bash
set -eo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${SCRIPT_DIR}/go-mk-common.sh"

GO_MK_GO_FINDING_PATTERN='^[^[:space:]][^:]+:[0-9]+:[0-9]+: |^[^[:space:]].*\([[:alnum:]_-]+\)$'
GO_MK_GO_LOCATION_PATTERN='^[^[:space:]][^:]+:[0-9]+:[0-9]+:'

golangci_command() {
    local mode
    local flags_text
    local targets_text
    local flag_args
    local target_args

    mode="$1"
    flags_text="${GOLANGCI_LINT_RUN_FLAGS:-${GOLANGCI_LINT_FLAGS:-}}"
    if [[ "${mode}" == "fmt" ]]; then
        flags_text="${GOLANGCI_LINT_FLAGS:-}"
    fi
    targets_text="${GOLANGCI_LINT_TARGETS:-./...}"

    go_mk_split_words "${flags_text}"
    flag_args=("${GO_MK_WORDS[@]}")
    go_mk_split_words "${targets_text}"
    target_args=("${GO_MK_WORDS[@]}")

    if [[ "${mode}" == "fmt" ]]; then
        GO_MK_WORDS=("${GOLANGCI_LINT:-golangci-lint}" fmt "${flag_args[@]}" "${target_args[@]}")
    else
        GO_MK_WORDS=("${GOLANGCI_LINT:-golangci-lint}" run "${flag_args[@]}" "${target_args[@]}")
    fi
    GO_MK_WORDS_COUNT=${#GO_MK_WORDS[@]}
}

capture_golangci_findings() {
    local raw_output
    local findings_output
    local exclude_pattern

    raw_output="$1"
    findings_output="$2"
    exclude_pattern=$(go_mk_exclude_pattern "${GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${GOLANGCI_LINT_EXCLUDE_PATHS:-}")

    golangci_command run
    go_mk_run_lint_capture "${raw_output}" "${GO_MK_WORDS[@]}"
    go_mk_extract_findings "${raw_output}" "${findings_output}" "${GO_MK_GO_FINDING_PATTERN}" "${exclude_pattern}"
}

capture_golangci_baseline_findings() {
    local raw_output
    local findings_output
    local run_count
    local run_index
    local run_raw_output
    local run_findings_output
    local merged_output
    local last_status

    raw_output="$1"
    findings_output="$2"
    run_count="${GOLANGCI_LINT_BASELINE_RUNS:-3}"
    last_status=0
    : > "${raw_output}"
    : > "${findings_output}"

    for run_index in $(seq 1 "${run_count}"); do
        run_raw_output=".make/golangci-lint-baseline.${run_index}.raw.out"
        run_findings_output=".make/golangci-lint-baseline.${run_index}.out"
        capture_golangci_findings "${run_raw_output}" "${run_findings_output}"
        if [[ "${GO_MK_COMMAND_STATUS}" -ne 0 ]]; then
            last_status="${GO_MK_COMMAND_STATUS}"
        fi
        cat "${run_raw_output}" >> "${raw_output}"
        cat "${run_findings_output}" >> "${findings_output}"
    done

    merged_output="${findings_output}.merged"
    sort -u "${findings_output}" > "${merged_output}"
    mv "${merged_output}" "${findings_output}"
    GO_MK_COMMAND_STATUS="${last_status}"
}

run_lint_tools() {
    go_mk_install_go_tool "${GOLANGCI_LINT_INSTALL:-github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4}"
    go_mk_install_go_tool "${GOFUMPT_INSTALL:-mvdan.cc/gofumpt@v0.9.2}"
    go_mk_install_go_tool "${GOIMPORTS_INSTALL:-golang.org/x/tools/cmd/goimports@v0.44.0}"
}

run_lint_golangci() {
    local raw_output
    local findings_output
    local exclude_pattern
    local run_status

    mkdir -p .make
    raw_output=".make/golangci-lint.raw.out"
    findings_output=".make/golangci-lint.out"
    exclude_pattern=$(go_mk_exclude_pattern "${GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${GOLANGCI_LINT_EXCLUDE_PATHS:-}")
    capture_golangci_findings "${raw_output}" "${findings_output}"
    run_status="${GO_MK_COMMAND_STATUS}"

    if ! go_mk_run_baseline_diff_gate \
        "golangci-lint" \
        "${findings_output}" \
        "${GOLANGCI_LINT_BASELINE:-.golangci-lint-baseline.txt}" \
        "golangci-lint" \
        "Fix these findings in code. Do not disable, silence, weaken, or otherwise circumvent the checks." \
        "${exclude_pattern}"; then
        return 1
    fi

    if [[ "${run_status}" -ne 0 && ! -s "${findings_output}" ]]; then
        printf "golangci-lint: FAILED\n"
        printf "  Exit status: %s\n\n" "${run_status}"
        printf "Output:\n"
        cat "${raw_output}"
        go_mk_record_failed_gate "golangci-lint"
        return "${run_status}"
    fi
}

run_lint_format() {
    local output_file

    mkdir -p .make
    output_file=".make/lint-format.out"
    golangci_command fmt
    GO_MK_WORDS=("${GO_MK_WORDS[@]:0:2}" --diff "${GO_MK_WORDS[@]:2}")
    go_mk_run_lint_capture "${output_file}" "${GO_MK_WORDS[@]}"

    if [[ -s "${output_file}" ]]; then
        printf "golangci-lint formatters need to update:\n"
        cat "${output_file}"
        printf "run make fmt\n"
        go_mk_record_failed_gate "lint-format"
        return 1
    fi

    if [[ "${GO_MK_COMMAND_STATUS}" -ne 0 ]]; then
        printf "lint-format: FAILED\n"
        printf "  Exit status: %s\n" "${GO_MK_COMMAND_STATUS}"
        go_mk_record_failed_gate "lint-format"
        return "${GO_MK_COMMAND_STATUS}"
    fi
}

capture_gocyclo_findings() {
    local raw_output
    local findings_output
    local exclude_pattern
    local transformed_output
    local normalized_output
    local filtered_output
    local targets_text
    local target_args
    local gocyclo_path
    local threshold

    raw_output="$1"
    findings_output="$2"
    threshold="${GOCYCLO_OVER:-30}"
    exclude_pattern=$(go_mk_exclude_pattern "${GOCYCLO_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${GOCYCLO_EXCLUDE_PATHS:-}")
    targets_text="${GOCYCLO_TARGETS:-}"
    if [[ -z "${targets_text}" ]]; then
        targets_text='$(find . -name "*.go" -not -name "*_test.go" -not -path "./vendor/*" -not -path "./gen/*" -not -path "./third_party/*")'
    fi
    go_mk_split_words "${targets_text}"
    target_args=("${GO_MK_WORDS[@]}")
    gocyclo_path="$(go env GOPATH)/bin/gocyclo"
    go_mk_run_lint_capture "${raw_output}" "${gocyclo_path}" -over "${threshold}" "${target_args[@]}"

    go_mk_setup_temp_dir
    transformed_output="${GO_MK_TEMP_DIR}/gocyclo.transformed.out"
    normalized_output="${GO_MK_TEMP_DIR}/gocyclo.normalized.out"
    filtered_output="${GO_MK_TEMP_DIR}/gocyclo.filtered.out"
    awk -v threshold="${threshold}" '
        NF >= 4 {
            complexity = $1
            location = $NF
            symbol = ""
            for (field_index = 2; field_index < NF; field_index++) {
                if (symbol != "") {
                    symbol = symbol " "
                }
                symbol = symbol $field_index
            }
            printf "%s: gocyclo: complexity %s over %s in %s\n", location, complexity, threshold, symbol
        }
    ' "${raw_output}" > "${transformed_output}"
    go_mk_normalize_file "${transformed_output}" "${normalized_output}"
    go_mk_filter_file "${normalized_output}" "${filtered_output}" "${exclude_pattern}"
    sort -u "${filtered_output}" > "${findings_output}"
}

run_lint_gocyclo() {
    local raw_output
    local findings_output
    local exclude_pattern
    local run_status

    mkdir -p .make
    raw_output=".make/gocyclo.raw.out"
    findings_output=".make/gocyclo.out"
    exclude_pattern=$(go_mk_exclude_pattern "${GOCYCLO_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${GOCYCLO_EXCLUDE_PATHS:-}")
    go_mk_install_go_tool "${GOCYCLO_INSTALL:-github.com/fzipp/gocyclo/cmd/gocyclo@latest}"
    capture_gocyclo_findings "${raw_output}" "${findings_output}"
    run_status="${GO_MK_COMMAND_STATUS}"

    if ! go_mk_run_baseline_diff_gate \
        "gocyclo" \
        "${findings_output}" \
        "${GOCYCLO_BASELINE:-.gocyclo-baseline.txt}" \
        "gocyclo" \
        "Reduce the reported cyclomatic complexity or accept the finding into the guarded baseline." \
        "${exclude_pattern}"; then
        return 1
    fi

    if [[ "${run_status}" -ne 0 && ! -s "${findings_output}" ]]; then
        printf "gocyclo: FAILED\n"
        printf "  Exit status: %s\n\n" "${run_status}"
        printf "Output:\n"
        cat "${raw_output}"
        go_mk_record_failed_gate "gocyclo"
        return "${run_status}"
    fi
}

capture_deadcode_findings() {
    local raw_output
    local findings_output
    local exclude_pattern
    local target_args
    local deadcode_path

    raw_output="$1"
    findings_output="$2"
    exclude_pattern=$(go_mk_exclude_pattern "${DEADCODE_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${DEADCODE_EXCLUDE_PATHS:-}")
    go_mk_split_words "${DEADCODE_TARGETS:-./...}"
    target_args=("${GO_MK_WORDS[@]}")
    deadcode_path="$(go env GOPATH)/bin/deadcode"
    go_mk_run_lint_capture "${raw_output}" "${deadcode_path}" "${target_args[@]}"
    go_mk_extract_findings "${raw_output}" "${findings_output}" "${GO_MK_GO_LOCATION_PATTERN}" "${exclude_pattern}"
}

run_lint_deadcode() {
    local raw_output
    local findings_output
    local exclude_pattern

    mkdir -p .make
    raw_output=".make/deadcode.raw.out"
    findings_output=".make/deadcode.out"
    exclude_pattern=$(go_mk_exclude_pattern "${DEADCODE_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${DEADCODE_EXCLUDE_PATHS:-}")
    go_mk_install_go_tool "${DEADCODE_INSTALL:-golang.org/x/tools/cmd/deadcode@latest}"
    capture_deadcode_findings "${raw_output}" "${findings_output}"
    go_mk_run_baseline_diff_gate \
        "deadcode" \
        "${findings_output}" \
        "${DEADCODE_BASELINE:-.deadcode-baseline.txt}" \
        "deadcode" \
        "The deadcode lint gate found unreachable code. Remove the reported code." \
        "${exclude_pattern}"
}

scoped_packages_from_files() {
    local files_text

    files_text="$1"
    printf "%s\n" ${files_text} \
        | xargs -n1 dirname \
        | sort -u \
        | awk '{ print "./" $0 }' \
        | tr "\n" " "
}

filter_scoped_findings() {
    local files_text

    files_text="$1"
    awk -v files="${files_text}" '
        BEGIN {
            file_count = split(files, keep_files, /[ \t]+/)
            for (file_index = 1; file_index <= file_count; file_index++) {
                if (keep_files[file_index] != "") {
                    keep[keep_files[file_index]] = 1
                }
            }
        }
        {
            for (file_path in keep) {
                if (index($0, file_path ":") == 1) {
                    print
                    next
                }
            }
        }
    '
}

filter_line_scoped_findings() {
    local ranges_file
    local awk_file

    ranges_file="$1"
    if [[ ! -s "${ranges_file}" ]]; then
        return
    fi
    awk_file=$(go_mk_findings_awk)
    awk -v action=linefilter -f "${awk_file}" "${ranges_file}" -
}

run_scoped_gate() {
    local gate_name
    local raw_output
    local findings_output
    local filtered_output
    local files_text
    local baseline_file
    local exclude_pattern
    local ranges_file
    local scope_label

    gate_name="$1"
    raw_output="$2"
    findings_output="$3"
    files_text="$4"
    baseline_file="$5"
    exclude_pattern="$6"
    ranges_file="${7:-}"
    filtered_output="${findings_output}.scoped"
    scope_label="listed files"

    go_mk_extract_findings "${raw_output}" "${findings_output}" "${GO_MK_GO_LOCATION_PATTERN}" "${exclude_pattern}"
    if [[ -n "${ranges_file}" ]]; then
        scope_label="staged lines"
        filter_line_scoped_findings "${ranges_file}" < "${findings_output}" > "${filtered_output}"
    else
        filter_scoped_findings "${files_text}" < "${findings_output}" > "${filtered_output}"
    fi

    if [[ ! -s "${filtered_output}" ]]; then
        printf "%s: OK (0 findings on %s)\n" "${gate_name}" "${scope_label}"
        return 0
    fi

    if [[ -z "${BASELINE:-}" ]]; then
        printf "%s findings on %s:\n" "${gate_name}" "${scope_label}"
        cat "${filtered_output}"
        return 1
    fi

    go_mk_run_baseline_diff_gate \
        "${gate_name}" \
        "${filtered_output}" \
        "${baseline_file}" \
        "${gate_name}" \
        "Fix these findings in code. Do not disable, silence, weaken, or otherwise circumvent the checks." \
        "${exclude_pattern}"
}

run_lint_files() {
    local files_text
    local package_text
    local package_args
    local status
    local golangci_raw
    local staticcheck_raw
    local golangci_flags
    local flag_args
    local staticcheck_flags
    local staticcheck_flag_args
    local staticcheck_bin
    local line_ranges_file

    files_text="${LINT_FILES:-./...}"
    if [[ -z "${files_text}" ]]; then
        printf "lint-files: LINT_FILES is empty\n"
        return 0
    fi

    mkdir -p .make
    status=0
    package_text=$(scoped_packages_from_files "${files_text}")
    go_mk_split_words "${package_text}"
    package_args=("${GO_MK_WORDS[@]}")
    golangci_raw=".make/lint-files.golangci.raw.out"
    staticcheck_raw=".make/lint-files.staticcheck.raw.out"
    line_ranges_file="${LINT_LINE_RANGES:-}"

    golangci_flags="${GOLANGCI_LINT_RUN_FLAGS:-${GOLANGCI_LINT_FLAGS:-}}"
    go_mk_split_words "${golangci_flags}"
    flag_args=("${GO_MK_WORDS[@]}")
    go_mk_run_lint_capture "${golangci_raw}" "${GOLANGCI_LINT:-golangci-lint}" run "${flag_args[@]}" "${package_args[@]}"
    run_scoped_gate \
        "golangci-lint" \
        "${golangci_raw}" \
        ".make/lint-files.golangci.out" \
        "${files_text}" \
        "${GOLANGCI_LINT_BASELINE:-.golangci-lint-baseline.txt}" \
        "$(go_mk_exclude_pattern "${GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${GOLANGCI_LINT_EXCLUDE_PATHS:-}")" \
        "${line_ranges_file}" || status=1

    staticcheck_bin="${STATICCHECK_EXTRA_BIN:-}"
    if [[ -z "${staticcheck_bin}" && -x .make/staticcheck-extra ]]; then
        staticcheck_bin=".make/staticcheck-extra"
    fi
    if [[ -n "${staticcheck_bin}" && -x "${staticcheck_bin}" ]]; then
        staticcheck_flags="${STATICCHECK_EXTRA_FLAGS:-}"
        go_mk_split_words "${staticcheck_flags}"
        staticcheck_flag_args=("${GO_MK_WORDS[@]}")
        go_mk_run_lint_capture "${staticcheck_raw}" "${staticcheck_bin}" "${staticcheck_flag_args[@]}" "${package_args[@]}"
        run_scoped_gate \
            "staticcheck-extra" \
            "${staticcheck_raw}" \
            ".make/lint-files.staticcheck.out" \
            "${files_text}" \
            "${STATICCHECK_EXTRA_BASELINE:-.staticcheck-extra-baseline.txt}" \
            "$(go_mk_exclude_pattern "${STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${STATICCHECK_EXTRA_EXCLUDE_PATHS:-}")" \
            "${line_ranges_file}" || status=1
    else
        printf "staticcheck-extra: not configured (skipped)\n"
    fi

    if [[ "${status}" -ne 0 && -n "${BASELINE:-}" ]]; then
        printf "\nRun with BASELINE=\"\" to see all findings without the baseline gate.\n"
    fi
    return "${status}"
}

run_lint_diff() {
    local files_text
    local diff_output
    local error_file
    local patch_file
    local ranges_file

    go_mk_setup_temp_dir
    error_file="${GO_MK_TEMP_DIR}/git-diff.err"
    patch_file="${GO_MK_TEMP_DIR}/git-diff.patch"
    ranges_file=".make/lint-diff.ranges"
    diff_output=$(git diff --cached --name-only --relative --diff-filter=ACM 2>"${error_file}" | grep "\.go$" | tr "\n" " " || true)
    files_text="${diff_output}"
    if [[ -z "${files_text}" ]]; then
        printf "lint-diff: no staged .go files\n"
        return 0
    fi
    git diff --cached --unified=0 --relative --diff-filter=ACM -- "*.go" > "${patch_file}" 2>"${error_file}"
    awk -v action=ranges -f "$(go_mk_findings_awk)" "${patch_file}" > "${ranges_file}"
    if [[ ! -s "${ranges_file}" ]]; then
        printf "lint-diff: no staged Go line changes\n"
        return 0
    fi
    BASELINE="${BASELINE:-1}" LINT_FILES="${files_text}" LINT_LINE_RANGES="${ranges_file}" run_lint_files
}

run_lint_chain() {
    local lint_output
    local status
    local gate_name
    local gate_output
    local gate_error
    local gate_status
    local make_args
    local gate_list
    local bypass_value
    local expected_raw
    local expected_error
    local expected_value

    mkdir -p .make
    rm -f .make/lint.failed
    lint_output=".make/lint.output"
    : > "${lint_output}"
    status=0
    go_mk_split_words "${GO_MK_RECURSIVE_MAKE_ARGS:-}"
    make_args=("${GO_MK_WORDS[@]}")
    gate_list="${LINT_GATES:-lint-golangci lint-format lint-gocyclo lint-deadcode staticcheck-extra}"

    for gate_name in ${gate_list}; do
        gate_output=".make/${gate_name}.aggregate.out"
        gate_error=".make/${gate_name}.aggregate.err"
        gate_status=0
        printf "lint: running %s\n" "${gate_name}"
        GO_MK_SKIP_FETCH=1 "${GO_MK_RECURSIVE_MAKE:-${MAKE:-make}}" "${make_args[@]}" --no-print-directory "${gate_name}" > "${gate_output}" 2>"${gate_error}" || gate_status=$?
        cat "${gate_output}" >> "${lint_output}"
        cat "${gate_error}" >> "${lint_output}"
        if [[ "${gate_status}" -ne 0 ]]; then
            status="${gate_status}"
        fi
    done

    awk '!/^make(\[[0-9]+\])?: \*\*\* \[[^]]+\] Error [0-9]+$/' "${lint_output}"
    if [[ "${status}" -eq 0 ]]; then
        return 0
    fi

    if [[ -s .make/lint.failed ]]; then
        failed_gates=$(awk 'NF && !seen[$0]++ { if (out != "") out = out ", "; out = out $0 } END { print out }' .make/lint.failed)
        printf "\nlint: FAILED\n"
        printf "  Failed gates: %s\n" "${failed_gates}"
    else
        printf "\nlint: FAILED\n"
        printf "  Failed gates: see failed target output above\n"
    fi

    bypass_value=$(printf "%s" "${BYPASS_LINT:-}" | go_mk_slugify)
    if [[ -n "${bypass_value}" ]]; then
        go_mk_setup_temp_dir
        expected_raw="${GO_MK_TEMP_DIR}/bypass-token.raw"
        expected_error="${GO_MK_TEMP_DIR}/bypass-token.err"
        if eval "${BYPASS_TOKEN_CMD:-${GO_MK_GATE_TOKEN_CMD:-}}" > "${expected_raw}" 2>"${expected_error}"; then
            expected_value=$(go_mk_slugify < "${expected_raw}" || true)
            if [[ -n "${expected_value}" && "${bypass_value}" == "${expected_value}" && "${BYPASS_CONFIRM:-}" == "1" ]]; then
                printf "\n***********************************************************************\n"
                printf "*** LINT FINDINGS NON-BLOCKING via BYPASS_LINT=%s\n" "${expected_value}"
                printf "*** Findings reported above but build proceeds. Do not merge without fixing.\n"
                printf "***********************************************************************\n\n"
                return 0
            fi
        fi
    fi

    return "${status}"
}

run_fmt() {
    golangci_command fmt
    go_mk_run_lint_cpu "${GO_MK_WORDS[@]}"
}

run_vet() {
    go_mk_split_words "${GO_VET_TARGETS:-./...}"
    go_mk_run_lint_cpu go vet "${GO_MK_WORDS[@]}"
}

run_test() {
    go_mk_split_words "${GO_TEST_TARGETS:-./...}"
    go_mk_run_lint_cpu go test "${GO_MK_WORDS[@]}"
}

run_govulncheck() {
    go_mk_install_go_tool "golang.org/x/vuln/cmd/govulncheck@latest"
    go_mk_split_words "${GOVULNCHECK_TARGETS:-./...}"
    go_mk_run_lint_cpu "$(go env GOPATH)/bin/govulncheck" "${GO_MK_WORDS[@]}"
}

command_name="${1:-}"
case "${command_name}" in
    lint-tools)
        run_lint_tools
        ;;
    lint)
        run_lint_chain
        ;;
    lint-golangci)
        run_lint_golangci
        ;;
    capture-golangci)
        mkdir -p .make
        capture_golangci_findings "${2:-.make/golangci-lint.raw.out}" "${3:-.make/golangci-lint.out}"
        ;;
    capture-golangci-baseline)
        mkdir -p .make
        capture_golangci_baseline_findings "${2:-.make/golangci-lint-baseline.raw.out}" "${3:-.make/golangci-lint-baseline.out}"
        ;;
    lint-format)
        run_lint_format
        ;;
    lint-gocyclo)
        run_lint_gocyclo
        ;;
    capture-gocyclo)
        mkdir -p .make
        go_mk_install_go_tool "${GOCYCLO_INSTALL:-github.com/fzipp/gocyclo/cmd/gocyclo@latest}"
        capture_gocyclo_findings "${2:-.make/gocyclo.raw.out}" "${3:-.make/gocyclo.out}"
        ;;
    lint-deadcode)
        run_lint_deadcode
        ;;
    capture-deadcode)
        mkdir -p .make
        go_mk_install_go_tool "${DEADCODE_INSTALL:-golang.org/x/tools/cmd/deadcode@latest}"
        capture_deadcode_findings "${2:-.make/deadcode.raw.out}" "${3:-.make/deadcode.out}"
        ;;
    lint-files)
        run_lint_files
        ;;
    lint-diff)
        run_lint_diff
        ;;
    fmt)
        run_fmt
        ;;
    vet)
        run_vet
        ;;
    test)
        run_test
        ;;
    govulncheck)
        run_govulncheck
        ;;
    *)
        printf "go-mk-lint: unknown command %s\n" "${command_name}"
        exit 2
        ;;
esac
