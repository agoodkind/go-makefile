#!/usr/bin/env bash
set -euo pipefail
set -f

TEMP_FILES=()

cleanup() {
    local temp_file

    set +u
    for temp_file in "${TEMP_FILES[@]}"; do
        rm -f "${temp_file}"
    done
}

trap cleanup EXIT

new_temp_file() {
    local temp_file

    temp_file="$(mktemp "${TMPDIR:-/tmp}/go-mk-cache-manifest.XXXXXX")"
    TEMP_FILES+=("${temp_file}")
    printf '%s\n' "${temp_file}"
}

write_output() {
    local name="$1"
    local value="$2"

    if [[ -z "${GITHUB_OUTPUT:-}" ]]; then
        return
    fi

    {
        printf '%s<<__GO_MK_CACHE_OUTPUT__\n' "${name}"
        printf '%s\n' "${value}"
        printf '__GO_MK_CACHE_OUTPUT__\n'
    } >> "${GITHUB_OUTPUT}"
}

normalize_fields() {
    local value="$1"
    local field

    for field in ${value}; do
        if [[ -z "${field}" || "${field}" == "." ]]; then
            continue
        fi
        printf '%s\n' "${field}" | sed 's#//*#/#g; s#^\./##; s#/$##'
    done
}

repo_prefix() {
    local prefix

    prefix="$(git rev-parse --show-prefix 2>/dev/null || true)"
    printf '%s\n' "${prefix%/}"
}

deepest_existing_parent() {
    local path="$1"
    local parent

    parent="$(dirname "${path}")"
    while [[ "${parent}" != "." && "${parent}" != "/" ]]; do
        if [[ -e "${parent}" ]]; then
            printf '%s\n' "${parent}"
            return
        fi
        parent="$(dirname "${parent}")"
    done

    if [[ -e "." ]]; then
        printf '%s\n' "."
    fi
}

path_is_tracked() {
    local path="$1"
    local parent
    local git_root
    local relative_path
    local absolute_path

    parent="$(deepest_existing_parent "${path}")"
    if [[ -z "${parent}" ]]; then
        return 1
    fi

    git_root="$(git -C "${parent}" rev-parse --show-toplevel 2>/dev/null || true)"
    if [[ -z "${git_root}" ]]; then
        return 1
    fi

    absolute_path="${path}"
    if [[ "${path}" != /* ]]; then
        absolute_path="$(pwd -P)/${path}"
    fi
    relative_path="${absolute_path#"${git_root}"/}"

    if git -C "${git_root}" ls-files --error-unmatch "${relative_path}" >/dev/null 2>&1; then
        return 0
    fi

    [[ -n "$(git -C "${git_root}" ls-files -- "${relative_path}")" ]]
}

filter_generated_outputs() {
    local outputs="$1"
    local output
    local warnings_file="$2"

    while IFS= read -r output; do
        if [[ -z "${output}" ]]; then
            continue
        fi
        if path_is_tracked "${output}"; then
            printf 'go-mk-cache-manifest: skip tracked generated output: %s\n' "${output}" >&2
            printf 'tracked output skipped: %s\n' "${output}" >> "${warnings_file}"
            continue
        fi
        printf '%s\n' "${output}"
    done < <(normalize_fields "${outputs}")
}

append_file_hash() {
    local manifest_file="$1"
    local path="$2"

    if [[ ! -f "${path}" ]]; then
        return
    fi

    printf 'file\t%s\t' "${path}" >> "${manifest_file}"
    shasum -a 256 "${path}" | awk '{print $1}' >> "${manifest_file}"
}

append_candidate_path() {
    local path_list_file="$1"
    local path="$2"

    if [[ -z "${path}" || ! -f "${path}" ]]; then
        return
    fi

    printf '%s\n' "${path}" >> "${path_list_file}"
}

append_tracked_hashes() {
    local manifest_file="$1"
    local path_list_file="$2"
    local path

    sort -u "${path_list_file}" | while IFS= read -r path; do
        if [[ -z "${path}" || ! -f "${path}" ]]; then
            continue
        fi
        append_file_hash "${manifest_file}" "${path}"
    done
}

collect_tracked_inputs() {
    local path_list_file="$1"
    local input

    git ls-files -- Makefile '*.mk' go.mod go.sum go.work go.work.sum .gitmodules buf.yaml buf.gen.yaml >> "${path_list_file}" 2>/dev/null || true

    while IFS= read -r input; do
        if [[ -z "${input}" ]]; then
            continue
        fi
        git ls-files -- "${input}" >> "${path_list_file}" 2>/dev/null || true
    done < <(normalize_fields "${GO_MK_GENERATE_INPUTS:-}")
}

append_go_mk_implementation_hashes() {
    local manifest_file="$1"
    local path_list_file
    local script_file

    path_list_file="$(new_temp_file)"

    append_candidate_path "${path_list_file}" "${GO_MK_SELF:-}"
    append_candidate_path "${path_list_file}" ".make/go.mk"

    for script_file in ${GO_MK_SCRIPT_FILES:-}; do
        append_candidate_path "${path_list_file}" ".make/${script_file}"
        if [[ -n "${GO_MK_DEV_DIR:-}" ]]; then
            append_candidate_path "${path_list_file}" "${GO_MK_DEV_DIR}/${script_file}"
        fi
        if [[ -n "${GO_MK_SELF_DIR:-}" ]]; then
            append_candidate_path "${path_list_file}" "${GO_MK_SELF_DIR}/${script_file}"
        fi
    done

    append_tracked_hashes "${manifest_file}" "${path_list_file}"
}

append_submodule_status() {
    local manifest_file="$1"

    if [[ ! -f ".gitmodules" ]]; then
        return
    fi

    printf 'submodules_begin\n' >> "${manifest_file}"
    git submodule status --recursive >> "${manifest_file}" 2>/dev/null || true
    printf 'submodules_end\n' >> "${manifest_file}"
}

build_manifest() {
    local manifest_file="$1"
    local output_paths="$2"
    local output_path

    {
        printf 'repo_prefix\t%s\n' "$(repo_prefix)"
        printf 'generate\t%s\n' "${GO_MK_GENERATE:-}"
        printf 'generate_inputs_begin\n'
        normalize_fields "${GO_MK_GENERATE_INPUTS:-}"
        printf 'generate_inputs_end\n'
        printf 'generate_outputs_begin\n'
        printf '%s\n' "${output_paths}"
        printf 'generate_outputs_end\n'
        printf 'workspace_use\t%s\n' "${GO_MK_WORKSPACE_USE:-}"
        printf 'tree_sitter_abi\t%s\n' "${TREE_SITTER_ABI:-}"
        printf 'go_mk_api_repo\t%s\n' "${GO_MK_API_REPO:-}"
        printf 'go_mk_api_ref\t%s\n' "${GO_MK_API_REF:-}"
        while IFS= read -r output_path; do
            if [[ -n "${output_path}" ]]; then
                printf 'output_path\t%s\n' "${output_path}"
            fi
        done <<< "${output_paths}"
    } >> "${manifest_file}"

    append_submodule_status "${manifest_file}"
}

main() {
    local warnings_file
    local paths_file
    local manifest_file
    local generated_cache_enabled="false"
    local generated_cache_requires_submodules="false"
    local output_paths
    local generated_cache_key=""

    warnings_file="$(new_temp_file)"
    paths_file="$(new_temp_file)"
    manifest_file="$(new_temp_file)"

    output_paths="$(filter_generated_outputs "${GO_MK_GENERATE_OUTPUTS:-}" "${warnings_file}")"

    if [[ -n "${GO_MK_GENERATE:-}" && -n "${GO_MK_GENERATE_INPUTS:-}" && -n "${output_paths}" ]]; then
        generated_cache_enabled="true"
    fi

    if [[ "${generated_cache_enabled}" == "true" && -f ".gitmodules" ]]; then
        generated_cache_requires_submodules="true"
    fi

    build_manifest "${manifest_file}" "${output_paths}"
    collect_tracked_inputs "${paths_file}"
    append_tracked_hashes "${manifest_file}" "${paths_file}"
    append_go_mk_implementation_hashes "${manifest_file}"
    generated_cache_key="$(shasum -a 256 "${manifest_file}" | awk '{print $1}')"

    write_output "generated_cache_enabled" "${generated_cache_enabled}"
    write_output "generated_cache_requires_submodules" "${generated_cache_requires_submodules}"
    write_output "generated_cache_paths" "${output_paths}"
    write_output "generated_cache_key" "${generated_cache_key}"
    write_output "generated_cache_warnings" "$(cat "${warnings_file}")"

    printf 'go-mk-cache-manifest: generated_cache_enabled=%s\n' "${generated_cache_enabled}"
    printf 'go-mk-cache-manifest: generated_cache_requires_submodules=%s\n' "${generated_cache_requires_submodules}"
    if [[ -n "${output_paths}" ]]; then
        printf 'go-mk-cache-manifest: generated output paths:\n%s\n' "${output_paths}"
    fi
    if [[ -s "${warnings_file}" ]]; then
        cat "${warnings_file}" >&2
    fi
}

main "$@"
