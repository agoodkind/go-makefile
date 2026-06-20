#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/go-mk-bootstrap-test.XXXXXX")"
GO_MK_BOOTSTRAP_BIN="${TEST_DIR}/go-mk"

cleanup() {
    rm -rf "${TEST_DIR}"
}

trap cleanup EXIT

run_bootstrap() {
    local repo_dir

    repo_dir="$1"
    shift

    (
        cd "${repo_dir}" || exit 1
        "${GO_MK_BOOTSTRAP_BIN}" bootstrap "$@"
    )
}

assert_file_exists() {
    local file_path

    file_path="$1"
    if [[ ! -f "${file_path}" ]]; then
        printf "missing expected file: %s\n" "${file_path}" >&2
        exit 1
    fi
}

assert_file_contains() {
    local file_path
    local expected_text

    file_path="$1"
    expected_text="$2"
    if ! grep -qF "${expected_text}" "${file_path}"; then
        printf "missing expected text %s in %s\n" "${expected_text}" "${file_path}" >&2
        exit 1
    fi
}

assert_files_same() {
    local expected_path
    local actual_path

    expected_path="$1"
    actual_path="$2"
    if ! cmp -s "${expected_path}" "${actual_path}"; then
        printf "files differ: %s %s\n" "${expected_path}" "${actual_path}" >&2
        exit 1
    fi
}

write_go_mod() {
    local repo_dir
    local module_path

    repo_dir="$1"
    module_path="$2"
    cat > "${repo_dir}/go.mod" <<EOF
module ${module_path}

go 1.26.4
EOF
}

test_assets_match_canonical_files() {
    assert_files_same \
        "${REPO_ROOT}/bootstrap.mk" \
        "${REPO_ROOT}/cmd/go-mk/bootstrap_assets/bootstrap.mk"
    assert_files_same \
        "${REPO_ROOT}/templates/Makefile.tmpl" \
        "${REPO_ROOT}/cmd/go-mk/bootstrap_assets/Makefile.tmpl"
}

test_new_binary_repo() {
    local repo_dir

    repo_dir="${TEST_DIR}/binary"
    mkdir -p "${repo_dir}"
    run_bootstrap "${repo_dir}" --module=goodkind.io/bootstrap-probe --binary --yes

    assert_file_exists "${repo_dir}/go.mod"
    assert_file_exists "${repo_dir}/Makefile"
    assert_file_exists "${repo_dir}/bootstrap.mk"
    assert_file_exists "${repo_dir}/.gitignore"
    assert_file_contains "${repo_dir}/Makefile" "BINARY := bootstrap-probe"
    assert_file_contains "${repo_dir}/Makefile" "GO_MK_MODULES := go-build.mk go-release.mk"
    assert_file_contains "${repo_dir}/bootstrap.mk" "GO_MK_BOOTSTRAP_FETCHED := 1"
    assert_file_contains "${repo_dir}/.gitignore" ".make/"

    GO_MK_DEV_DIR="${REPO_ROOT}" make -C "${repo_dir}" help >/dev/null
}

test_new_library_repo() {
    local repo_dir

    repo_dir="${TEST_DIR}/library"
    mkdir -p "${repo_dir}"
    run_bootstrap "${repo_dir}" --module=goodkind.io/bootstrap-library --library --yes

    assert_file_contains "${repo_dir}/Makefile" "LIBRARY := 1"
    assert_file_contains "${repo_dir}/Makefile" "GO_MK_MODULES := go-build.mk"
    assert_file_exists "${repo_dir}/bootstrap.mk"
}

test_custom_makefile_is_preserved() {
    local repo_dir

    repo_dir="${TEST_DIR}/custom"
    mkdir -p "${repo_dir}"
    write_go_mod "${repo_dir}" "goodkind.io/custom"
    cat > "${repo_dir}/Makefile" <<'EOF'
custom:
	@echo custom
EOF
    cp "${repo_dir}/Makefile" "${repo_dir}/Makefile.before"

    run_bootstrap "${repo_dir}" --yes

    assert_files_same "${repo_dir}/Makefile.before" "${repo_dir}/Makefile"
    assert_file_exists "${repo_dir}/bootstrap.mk"
    assert_file_contains "${repo_dir}/.gitignore" ".make/"
}

test_generated_rerun_is_stable() {
    local repo_dir

    repo_dir="${TEST_DIR}/rerun"
    mkdir -p "${repo_dir}"
    run_bootstrap "${repo_dir}" --module=goodkind.io/rerun --binary --yes
    cp "${repo_dir}/Makefile" "${repo_dir}/Makefile.before"
    cp "${repo_dir}/bootstrap.mk" "${repo_dir}/bootstrap.mk.before"
    cp "${repo_dir}/.gitignore" "${repo_dir}/.gitignore.before"

    run_bootstrap "${repo_dir}" --module=goodkind.io/rerun --binary --yes

    assert_files_same "${repo_dir}/Makefile.before" "${repo_dir}/Makefile"
    assert_files_same "${repo_dir}/bootstrap.mk.before" "${repo_dir}/bootstrap.mk"
    assert_files_same "${repo_dir}/.gitignore.before" "${repo_dir}/.gitignore"
}

test_generated_makefile_is_repaired() {
    local repo_dir

    repo_dir="${TEST_DIR}/repair"
    mkdir -p "${repo_dir}"
    run_bootstrap "${repo_dir}" --module=goodkind.io/repair --binary --yes
    grep -vF "include bootstrap.mk" "${repo_dir}/Makefile" > "${repo_dir}/Makefile.next"
    mv "${repo_dir}/Makefile.next" "${repo_dir}/Makefile"

    run_bootstrap "${repo_dir}" --module=goodkind.io/repair --binary --yes

    assert_file_contains "${repo_dir}/Makefile" "include bootstrap.mk"
}

build_local_go_mk() {
    (
        cd "${REPO_ROOT}" || exit 1
        go build -o "${GO_MK_BOOTSTRAP_BIN}" ./cmd/go-mk
    )
}

build_local_go_mk
test_assets_match_canonical_files
test_new_binary_repo
test_new_library_repo
test_custom_makefile_is_preserved
test_generated_rerun_is_stable
test_generated_makefile_is_repaired

printf "go-mk bootstrap test: OK\n"
