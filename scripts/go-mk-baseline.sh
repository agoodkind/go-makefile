#!/usr/bin/env bash
set -eo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${SCRIPT_DIR}/go-mk-common.sh"

# Components captured this run, collected as JSON manifest records and written by
# the go-mk binary in one batch so a single neutral roll-up prints.
GO_MK_BASELINE_MANIFEST=""

# json_escape escapes a value for embedding in a JSON string.
json_escape() {
    local value
    value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//	/\\t}"
    printf "%s" "${value}"
}

# emit_manifest_record appends one component record to the batch manifest. Called
# only after a component's gate passes and its findings are captured, so a
# skipped component contributes no record, preserving the token-gating flow.
emit_manifest_record() {
    local title baseline_file findings_file label mode exclude_pattern scope_pattern record

    title="$1"
    baseline_file="$2"
    findings_file="$3"
    label="$4"
    mode="$5"
    exclude_pattern="${6:-}"
    scope_pattern="${7:-}"

    record=$(printf '{"title":"%s","label":"%s","baselineFile":"%s","findingsFile":"%s","mode":"%s","excludePattern":"%s","scopePattern":"%s"}' \
        "$(json_escape "${title}")" \
        "$(json_escape "${label}")" \
        "$(json_escape "${baseline_file}")" \
        "$(json_escape "${findings_file}")" \
        "$(json_escape "${mode}")" \
        "$(json_escape "${exclude_pattern}")" \
        "$(json_escape "${scope_pattern}")")
    if [[ -z "${GO_MK_BASELINE_MANIFEST}" ]]; then
        GO_MK_BASELINE_MANIFEST="${record}"
    else
        GO_MK_BASELINE_MANIFEST="${GO_MK_BASELINE_MANIFEST},${record}"
    fi
}

# flush_manifest writes every collected component in one binary invocation. The
# binary owns the atomic write, the neutral counts, and the single roll-up.
flush_manifest() {
    local resolved_bin

    if [[ -z "${GO_MK_BASELINE_MANIFEST}" ]]; then
        return 0
    fi
    bash "${SCRIPT_DIR}/go-mk-bin.sh" bin
    resolved_bin=$(bash "${SCRIPT_DIR}/go-mk-bin.sh" selected-bin)
    if [[ -z "${resolved_bin}" || ! -x "${resolved_bin}" ]]; then
        printf "go-mk: could not resolve the baseline binary\n" >&2
        return 1
    fi
    printf '{"components":[%s]}' "${GO_MK_BASELINE_MANIFEST}" | "${resolved_bin}" write-batch --manifest -
}

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

# Queue one component for the batch write. The eighth positional argument
# (suppress_fixed_count) is accepted for call-site compatibility but no longer
# alters output: the neutral counts the binary prints never expose it.
write_component_baseline() {
    local title
    local baseline_file
    local findings_file
    local label
    local mode
    local exclude_pattern
    local scope_pattern

    title="$1"
    baseline_file="$2"
    findings_file="$3"
    label="$4"
    mode="$5"
    exclude_pattern="${6:-}"
    scope_pattern="${7:-}"
    emit_manifest_record "${title}" "${baseline_file}" "${findings_file}" "${label}" "${mode}" "${exclude_pattern}" "${scope_pattern}"
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
    bash "${SCRIPT_DIR}/go-mk-bin.sh" bin
    staticcheck_bin=$(bash "${SCRIPT_DIR}/go-mk-bin.sh" selected-bin)
    if [[ -z "${staticcheck_bin}" || ! -x "${staticcheck_bin}" ]]; then
        printf "go-mk: could not resolve the go-mk binary\n" >&2
        return 1
    fi
    "${staticcheck_bin}" staticcheck-extra-bin
    "${staticcheck_bin}" staticcheck-extra-capture "${raw_output}" "${findings_output}"
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

# Component updaters may refuse (e.g. a scope-guarded staticcheck full sync) or
# fail their gate. A single refusal must not abort the batch: every component
# that did pass still needs its queued record flushed and rolled up. So each
# updater runs tolerantly here and the worst status is carried to the exit.
update_status=0

case "${component}" in
    all)
        update_golangci_baseline "${mode}" || update_status=$?
        update_gocyclo_baseline "${mode}" || update_status=$?
        update_deadcode_baseline "${mode}" || update_status=$?
        update_staticcheck_baseline "${mode}" || update_status=$?
        ;;
    golangci)
        update_golangci_baseline "${mode}" || update_status=$?
        ;;
    golangci-scope)
        update_golangci_baseline_scope "${mode}" || update_status=$?
        ;;
    auto-baseline-scope)
        auto_baseline_golangci_scope || update_status=$?
        ;;
    gocyclo)
        update_gocyclo_baseline "${mode}" || update_status=$?
        ;;
    deadcode)
        update_deadcode_baseline "${mode}" || update_status=$?
        ;;
    staticcheck-extra)
        update_staticcheck_baseline "${mode}" || update_status=$?
        ;;
    *)
        printf "go-mk: unknown component %s\n" "${component}"
        exit 2
        ;;
esac

flush_manifest || update_status=$?
exit "${update_status}"
