#!/usr/bin/env bash
# go-mk-bootstrap.sh: obtain the go-mk engine, then provision every asset.
set -euo pipefail

GO_MK_API_REPO="${GO_MK_API_REPO:-agoodkind/go-makefile}"
GO_MK_API_REF="${GO_MK_API_REF:-main}"
GO_MK_RELEASE_BASE="${GO_MK_RELEASE_BASE:-https://github.com}"
GO_MK_DEV_DIR="${GO_MK_DEV_DIR:-}"
GO_MK_MODULES="${GO_MK_MODULES:-}"
MAKE_DIR=".make"
SELF_ASSET="scripts/go-mk-bootstrap.sh"

resolve_release_tag() {
    case "${GO_MK_API_REF}" in
        ""|main|rolling) printf '%s\n' "rolling" ;;
        *) printf '%s\n' "${GO_MK_API_REF}" ;;
    esac
}

resolve_go_mk_bin() {
    if [[ -n "${GO_MK_DEV_DIR}" ]]; then
        if [[ -x "${GO_MK_DEV_DIR}/.make/go-mk" ]]; then
            printf '%s\n' "${GO_MK_DEV_DIR}/.make/go-mk"
            return 0
        fi
        if command -v go >/dev/null 2>&1; then
            local dev_bin
            dev_bin=$(mktemp "${TMPDIR:-/tmp}/go-mk-dev.XXXXXXXX") || return 1
            if (cd "${GO_MK_DEV_DIR}" && go build -o "${dev_bin}" ./cmd/go-mk); then
                printf '%s\n' "${dev_bin}"
                return 0
            fi
            rm -f "${dev_bin}"
        fi
    fi
    if [[ -x "${MAKE_DIR}/go-mk" ]]; then
        printf '%s\n' "${MAKE_DIR}/go-mk"
        return 0
    fi
    printf '%s\n' "${MAKE_DIR}/go-mk"
}

download_go_mk() {
    local release_tag
    local archive_name
    local archive_url
    local checksums_url
    local tmp_dir=""
    local expected_sum
    local actual_sum

    release_tag=$(resolve_release_tag)
    archive_name="go-mk_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m).tar.gz"
    case "$(uname -m)" in
        x86_64) archive_name="go-mk_$(uname -s | tr '[:upper:]' '[:lower:]')_amd64.tar.gz" ;;
        aarch64|arm64) archive_name="go-mk_$(uname -s | tr '[:upper:]' '[:lower:]')_arm64.tar.gz" ;;
    esac
    archive_url="${GO_MK_RELEASE_BASE}/${GO_MK_API_REPO}/releases/download/${release_tag}/${archive_name}"
    checksums_url="${GO_MK_RELEASE_BASE}/${GO_MK_API_REPO}/releases/download/${release_tag}/checksums.txt"
    tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/go-mk-bootstrap.XXXXXXXX") || return 1
    trap '[[ -n "${tmp_dir:-}" ]] && rm -rf "${tmp_dir}"' RETURN

    if ! curl -fsSL --connect-timeout 5 --max-time 30 "${checksums_url}" -o "${tmp_dir}/checksums.txt"; then
        printf 'error: could not download checksums from %s\n' "${checksums_url}" >&2
        return 1
    fi
    if ! curl -fsSL --connect-timeout 5 --max-time 60 "${archive_url}" -o "${tmp_dir}/${archive_name}"; then
        printf 'error: could not download %s from %s\n' "${archive_name}" "${archive_url}" >&2
        return 1
    fi
    expected_sum=$(awk -v name="${archive_name}" '$2 == name { print $1; exit }' "${tmp_dir}/checksums.txt")
    if [[ -z "${expected_sum}" ]]; then
        printf 'error: checksums.txt has no entry for %s\n' "${archive_name}" >&2
        return 1
    fi
    if command -v shasum >/dev/null 2>&1; then
        actual_sum=$(shasum -a 256 "${tmp_dir}/${archive_name}" | awk '{ print $1 }')
    else
        actual_sum=$(sha256sum "${tmp_dir}/${archive_name}" | awk '{ print $1 }')
    fi
    if [[ "${actual_sum}" != "${expected_sum}" ]]; then
        printf 'error: %s checksum mismatch\n' "${archive_name}" >&2
        return 1
    fi
    mkdir -p "${MAKE_DIR}"
    tar -xzf "${tmp_dir}/${archive_name}" -C "${MAKE_DIR}" go-mk
    chmod +x "${MAKE_DIR}/go-mk"
}

main() {
    if [[ "${_GO_MK_PROVISIONED:-}" == "1" ]]; then
        if [[ ! -x "${MAKE_DIR}/go-mk" ]]; then
            printf 'error: _GO_MK_PROVISIONED=1 but %s/go-mk is missing\n' "${MAKE_DIR}" >&2
            return 1
        fi
        exec env \
            GO_MK_API_REPO="${GO_MK_API_REPO}" \
            GO_MK_API_REF="${GO_MK_API_REF}" \
            GO_MK_MODULES="${GO_MK_MODULES}" \
            GO_MK_RELEASE_BASE="${GO_MK_RELEASE_BASE}" \
            GO_MK_CODELOAD_BASE="${GO_MK_CODELOAD_BASE:-https://codeload.github.com}" \
            GO_MK_DEV_DIR="${GO_MK_DEV_DIR}" \
            _GO_MK_PROVISIONED="${_GO_MK_PROVISIONED}" \
            "${MAKE_DIR}/go-mk" provision
    fi
    go_mk_bin=$(resolve_go_mk_bin)
    if [[ ! -x "${go_mk_bin}" ]]; then
        if ! download_go_mk; then
            printf 'error: could not obtain go-mk engine binary\n' >&2
            return 1
        fi
        go_mk_bin="${MAKE_DIR}/go-mk"
    fi
    exec env \
        GO_MK_API_REPO="${GO_MK_API_REPO}" \
        GO_MK_API_REF="${GO_MK_API_REF}" \
        GO_MK_MODULES="${GO_MK_MODULES}" \
        GO_MK_RELEASE_BASE="${GO_MK_RELEASE_BASE}" \
        GO_MK_CODELOAD_BASE="${GO_MK_CODELOAD_BASE:-https://codeload.github.com}" \
        GO_MK_DEV_DIR="${GO_MK_DEV_DIR}" \
        _GO_MK_PROVISIONED="${_GO_MK_PROVISIONED:-}" \
        "${go_mk_bin}" provision
}

main "$@"
