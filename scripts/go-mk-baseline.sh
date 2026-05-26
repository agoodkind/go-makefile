#!/usr/bin/env bash
set -eo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${SCRIPT_DIR}/go-mk-common.sh"

run_gate() {
    local component_name
    local stamp_path

    component_name="$1"
    stamp_path=".make/${component_name}-baseline.gate.ok"
    bash "${SCRIPT_DIR}/go-mk-gate.sh" \
        --stamp "${stamp_path}" \
        --confirm-value "${BASELINE_CONFIRM:-}" \
        --token-value "${BASELINE_TOKEN:-}" \
        --token-command "${BASELINE_TOKEN_CMD:-${GO_MK_GATE_TOKEN_CMD:-}}"
    [[ -f "${stamp_path}" ]]
}

normalize_mode() {
    local mode

    mode="${1:-sync}"
    case "${mode}" in
        sync | prune-fixed | remove-fixed | accept-new)
            printf "%s\n" "${mode}"
            ;;
        *)
            printf "unknown baseline update mode: %s\n" "${mode}"
            exit 2
            ;;
    esac
}

write_component_baseline() {
    local title
    local baseline_file
    local findings_file
    local label
    local mode
    local exclude_pattern
    local scope_pattern
    local suppress_fixed_count
    local temporary_file

    title="$1"
    baseline_file="$2"
    findings_file="$3"
    label="$4"
    mode="$5"
    exclude_pattern="${6:-}"
    scope_pattern="${7:-}"
    suppress_fixed_count="${8:-}"
    mkdir -p "$(dirname "${baseline_file}")"
    if [[ ! -f "${baseline_file}" ]]; then
        : > "${baseline_file}"
    fi
    temporary_file="${baseline_file}.tmp"
    printf "%s baseline update\n" "${label}"
    printf "  File: %s\n" "${baseline_file}"
    printf "  Mode: %s\n" "${mode}"
    printf "  Scope: %s\n\n" "${scope_pattern:-all}"
    go_mk_print_baseline_update_counts "${label}" "${baseline_file}" "${findings_file}" "${label}" "${mode}" "${exclude_pattern}" "${scope_pattern}" "${suppress_fixed_count}"
    go_mk_write_baseline_file "${title}" "${baseline_file}" "${findings_file}" "${label}" "${temporary_file}" "${mode}" "${scope_pattern}"
    mv "${temporary_file}" "${baseline_file}"
    go_mk_print_baseline_overall_counts "${label}" "${baseline_file}" "${findings_file}" "${label}" "${exclude_pattern}" "${scope_pattern}"
    printf "\n%s: baseline %s refreshed\n" "${label}" "${baseline_file}"
}

update_golangci_baseline() {
    local mode
    local raw_output
    local findings_output
    local exclude_pattern

    mode="$1"
    if ! run_gate "golangci-lint"; then
        return 0
    fi
    mkdir -p .make
    raw_output=".make/golangci-lint-baseline.raw.out"
    findings_output=".make/golangci-lint-baseline.out"
    exclude_pattern=$(go_mk_exclude_pattern "${GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${GOLANGCI_LINT_EXCLUDE_PATHS:-}")
    bash "${SCRIPT_DIR}/go-mk-lint.sh" lint-tools
    bash "${SCRIPT_DIR}/go-mk-lint.sh" capture-golangci-baseline "${raw_output}" "${findings_output}"
    write_component_baseline \
        "golangci-lint" \
        "${GOLANGCI_LINT_BASELINE:-.golangci-lint-baseline.txt}" \
        "${findings_output}" \
        "golangci-lint" \
        "${mode}" \
        "${exclude_pattern}"
}

# Capture and write only the scoped slice of the golangci baseline. Shared by
# the token-gated manual scoped update and the automatic new-gate rollout. The
# scope_pattern is passed to write_component_baseline, so the scoped awk write
# preserves every out-of-scope baseline row byte-for-byte.
write_golangci_scope_baseline() {
    local mode
    local scope_pattern
    local raw_output
    local findings_output
    local exclude_pattern

    mode="$1"
    scope_pattern="$2"
    mkdir -p .make
    raw_output=".make/golangci-lint-scope-baseline.raw.out"
    findings_output=".make/golangci-lint-scope-baseline.out"
    exclude_pattern=$(go_mk_exclude_pattern "${GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${GOLANGCI_LINT_EXCLUDE_PATHS:-}")
    bash "${SCRIPT_DIR}/go-mk-lint.sh" lint-tools
    bash "${SCRIPT_DIR}/go-mk-lint.sh" capture-golangci-scope "${raw_output}" "${findings_output}"
    write_component_baseline \
        "golangci-lint" \
        "${GOLANGCI_LINT_BASELINE:-.golangci-lint-baseline.txt}" \
        "${findings_output}" \
        "golangci-lint" \
        "${mode}" \
        "${exclude_pattern}" \
        "${scope_pattern}"
}

# Token-gated manual scoped baseline for one linter or rule. Refuses to run
# unscoped, so a scoped target cannot silently full-sync the whole baseline.
update_golangci_baseline_scope() {
    local mode
    local scope_pattern

    mode="$1"
    scope_pattern=$(go_mk_golangci_baseline_scope_pattern)
    if [[ -z "${scope_pattern}" ]]; then
        printf "golangci-lint scope baseline: set LINTER=<name>, RULE=<name>, or GOLANGCI_LINT_BASELINE_SCOPE_PATTERN\n"
        return 1
    fi
    if ! run_gate "golangci-lint"; then
        return 0
    fi
    write_golangci_scope_baseline "${mode}" "${scope_pattern}"
}

# Automatic, token-free, strictly scope-limited golangci baseline used by the
# new-gate notice rollout. Safe without the token because the scoped write only
# adds the declared slice and leaves every other linter's rows untouched.
auto_baseline_golangci_scope() {
    local scope_pattern

    scope_pattern=$(go_mk_golangci_baseline_scope_pattern)
    if [[ -z "${scope_pattern}" ]]; then
        printf "auto-baseline: missing scope; set LINTER, RULE, or GOLANGCI_LINT_BASELINE_SCOPE_PATTERN\n"
        return 1
    fi
    write_golangci_scope_baseline "sync" "${scope_pattern}"
}

update_gocyclo_baseline() {
    local mode
    local raw_output
    local findings_output
    local exclude_pattern

    mode="$1"
    if ! run_gate "gocyclo"; then
        return 0
    fi
    mkdir -p .make
    raw_output=".make/gocyclo-baseline.raw.out"
    findings_output=".make/gocyclo-baseline.out"
    exclude_pattern=$(go_mk_exclude_pattern "${GOCYCLO_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${GOCYCLO_EXCLUDE_PATHS:-}")
    bash "${SCRIPT_DIR}/go-mk-lint.sh" capture-gocyclo "${raw_output}" "${findings_output}"
    write_component_baseline \
        "gocyclo" \
        "${GOCYCLO_BASELINE:-.gocyclo-baseline.txt}" \
        "${findings_output}" \
        "gocyclo" \
        "${mode}" \
        "${exclude_pattern}"
}

update_deadcode_baseline() {
    local mode
    local raw_output
    local findings_output
    local exclude_pattern

    mode="$1"
    if ! run_gate "deadcode"; then
        return 0
    fi
    mkdir -p .make
    raw_output=".make/deadcode-baseline.raw.out"
    findings_output=".make/deadcode-baseline.out"
    exclude_pattern=$(go_mk_exclude_pattern "${DEADCODE_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${DEADCODE_EXCLUDE_PATHS:-}")
    bash "${SCRIPT_DIR}/go-mk-lint.sh" capture-deadcode "${raw_output}" "${findings_output}"
    write_component_baseline \
        "deadcode" \
        "${DEADCODE_BASELINE:-.deadcode-baseline.txt}" \
        "${findings_output}" \
        "deadcode" \
        "${mode}" \
        "${exclude_pattern}"
}

update_staticcheck_baseline() {
    local mode
    local raw_output
    local findings_output
    local exclude_pattern
    local scope_pattern
    local suppress_fixed_count

    mode="$1"
    if ! run_gate "staticcheck-extra"; then
        return 0
    fi
    mkdir -p .make
    raw_output=".make/staticcheck-extra.raw.out"
    findings_output=".make/staticcheck-extra.out"
    exclude_pattern=$(go_mk_exclude_pattern "${STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS:-_test\\.go:}" "${STATICCHECK_EXTRA_EXCLUDE_PATHS:-}")
    scope_pattern=$(go_mk_staticcheck_baseline_scope_pattern)
    suppress_fixed_count=$(go_mk_staticcheck_suppress_fixed_count)
    if [[ -n "${STATICCHECK_EXTRA_FLAGS:-}" && -z "${scope_pattern}" ]]; then
        case "${mode}" in
            sync | prune-fixed | remove-fixed)
                printf "staticcheck-extra: refusing %s baseline update with STATICCHECK_EXTRA_FLAGS but no baseline scope pattern\n" "${mode}"
                printf "Set STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN to the intended finding regex, or unset STATICCHECK_EXTRA_FLAGS for a full baseline update.\n"
                return 1
                ;;
        esac
    fi
    bash "${SCRIPT_DIR}/go-mk-staticcheck-extra.sh" bin
    bash "${SCRIPT_DIR}/go-mk-staticcheck-extra.sh" capture "${raw_output}" "${findings_output}"
    write_component_baseline \
        "staticcheck-extra" \
        "${STATICCHECK_EXTRA_BASELINE:-.staticcheck-extra-baseline.txt}" \
        "${findings_output}" \
        "staticcheck-extra" \
        "${mode}" \
        "${exclude_pattern}" \
        "${scope_pattern}" \
        "${suppress_fixed_count}"
}

mode=$(normalize_mode "${BASELINE_UPDATE_MODE:-sync}")
component="${1:-all}"

case "${component}" in
    all)
        update_golangci_baseline "${mode}"
        update_gocyclo_baseline "${mode}"
        update_deadcode_baseline "${mode}"
        update_staticcheck_baseline "${mode}"
        ;;
    golangci)
        update_golangci_baseline "${mode}"
        ;;
    golangci-scope)
        update_golangci_baseline_scope "${mode}"
        ;;
    auto-baseline-scope)
        auto_baseline_golangci_scope
        ;;
    gocyclo)
        update_gocyclo_baseline "${mode}"
        ;;
    deadcode)
        update_deadcode_baseline "${mode}"
        ;;
    staticcheck-extra)
        update_staticcheck_baseline "${mode}"
        ;;
    *)
        printf "go-mk-baseline: unknown component %s\n" "${component}"
        exit 2
        ;;
esac
