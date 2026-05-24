#!/usr/bin/env bash
set -eo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${SCRIPT_DIR}/go-mk-common.sh"

TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/go-mk-baseline-test.XXXXXX")

cleanup() {
    rm -rf "${TEST_DIR}"
}

trap cleanup EXIT

write_input_files() {
    cat > "${TEST_DIR}/old.baseline" <<'EOF'
# sample: generated_at=2026-01-01T00:00:00Z
old.go:10:2: fixed finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
keep.go:20:2: existing finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
EOF

    cat > "${TEST_DIR}/current.findings" <<'EOF'
keep.go:21:2: existing finding
new.go:30:2: new finding
EOF

    cat > "${TEST_DIR}/old-scoped.baseline" <<'EOF'
# sample: generated_at=2026-01-01T00:00:00Z
old-scoped.go:10:2: fixed scoped_rule finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
old-other.go:11:2: unrelated saved finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
keep-scoped.go:20:2: existing scoped_rule finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
EOF

    cat > "${TEST_DIR}/current-scoped.findings" <<'EOF'
keep-scoped.go:21:2: existing scoped_rule finding
new-scoped.go:30:2: new scoped_rule finding
EOF
}

assert_contains() {
    local file_path
    local expected_text

    file_path="$1"
    expected_text="$2"
    if ! grep -q "${expected_text}" "${file_path}"; then
        printf "missing expected text %s in %s\n" "${expected_text}" "${file_path}"
        exit 1
    fi
}

assert_not_contains() {
    local file_path
    local unexpected_text

    file_path="$1"
    unexpected_text="$2"
    if grep -q "${unexpected_text}" "${file_path}"; then
        printf "found unexpected text %s in %s\n" "${unexpected_text}" "${file_path}"
        exit 1
    fi
}

run_case() {
    local mode
    local output_file

    mode="$1"
    output_file="${TEST_DIR}/${mode}.baseline"
    go_mk_write_baseline_file \
        "sample" \
        "${TEST_DIR}/old.baseline" \
        "${TEST_DIR}/current.findings" \
        "sample" \
        "${output_file}" \
        "${mode}"
    printf "%s\n" "${output_file}"
}

run_scoped_case() {
    local mode
    local output_file

    mode="$1"
    output_file="${TEST_DIR}/${mode}.scoped.baseline"
    go_mk_write_baseline_file \
        "sample" \
        "${TEST_DIR}/old-scoped.baseline" \
        "${TEST_DIR}/current-scoped.findings" \
        "sample" \
        "${output_file}" \
        "${mode}" \
        "scoped_rule"
    printf "%s\n" "${output_file}"
}

write_input_files

sync_output=$(run_case sync)
assert_contains "${sync_output}" "existing finding"
assert_contains "${sync_output}" "new finding"
assert_not_contains "${sync_output}" "fixed finding"

prune_output=$(run_case prune-fixed)
assert_contains "${prune_output}" "existing finding"
assert_not_contains "${prune_output}" "new finding"
assert_not_contains "${prune_output}" "fixed finding"

accept_output=$(run_case accept-new)
assert_contains "${accept_output}" "existing finding"
assert_contains "${accept_output}" "new finding"
assert_contains "${accept_output}" "fixed finding"

scoped_sync_output=$(run_scoped_case sync)
assert_contains "${scoped_sync_output}" "new scoped_rule finding"
assert_contains "${scoped_sync_output}" "unrelated saved finding"
assert_not_contains "${scoped_sync_output}" "fixed scoped_rule finding"

scoped_prune_output=$(run_scoped_case prune-fixed)
assert_contains "${scoped_prune_output}" "existing scoped_rule finding"
assert_contains "${scoped_prune_output}" "unrelated saved finding"
assert_not_contains "${scoped_prune_output}" "new scoped_rule finding"
assert_not_contains "${scoped_prune_output}" "fixed scoped_rule finding"

counts_output="${TEST_DIR}/scoped-counts.out"
go_mk_print_baseline_update_counts \
    "sample" \
    "${TEST_DIR}/old-scoped.baseline" \
    "${TEST_DIR}/current-scoped.findings" \
    "sample" \
    "sync" \
    "" \
    "scoped_rule" \
    "" > "${counts_output}"
assert_contains "${counts_output}" "This update:"
assert_contains "${counts_output}" "Findings captured: 2"
assert_contains "${counts_output}" "Keys added: 1"
assert_contains "${counts_output}" "Keys refreshed: 1"
assert_contains "${counts_output}" "Keys removed: 1"

suppress_output="${TEST_DIR}/suppress-counts.out"
go_mk_print_baseline_update_counts \
    "sample" \
    "${TEST_DIR}/old-scoped.baseline" \
    "${TEST_DIR}/current-scoped.findings" \
    "sample" \
    "sync" \
    "" \
    "" \
    "1" > "${suppress_output}"
assert_contains "${suppress_output}" "This update:"
assert_contains "${suppress_output}" "Findings captured: 2"
assert_contains "${suppress_output}" "Keys added: 1"
assert_contains "${suppress_output}" "Keys refreshed: 1"
assert_contains "${suppress_output}" "Keys removed: 0"

overall_output="${TEST_DIR}/overall-counts.out"
go_mk_write_baseline_file \
    "sample" \
    "${TEST_DIR}/old-scoped.baseline" \
    "${TEST_DIR}/current-scoped.findings" \
    "sample" \
    "${TEST_DIR}/overall.baseline" \
    "sync" \
    "scoped_rule"
go_mk_print_baseline_overall_counts \
    "sample" \
    "${TEST_DIR}/overall.baseline" \
    "${TEST_DIR}/current-scoped.findings" \
    "sample" \
    "" \
    "scoped_rule" > "${overall_output}"
assert_contains "${overall_output}" "Overall baseline:"
assert_contains "${overall_output}" "Current findings covered: 2"
assert_contains "${overall_output}" "Keys in this scope: 2"
assert_contains "${overall_output}" "Total keys: 3"

printf "go-mk-baseline test: OK\n"
