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

printf "go-mk-baseline test: OK\n"
