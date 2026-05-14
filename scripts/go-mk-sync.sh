#!/usr/bin/env bash
set -eo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
FETCH_SCRIPT="${SCRIPT_DIR}/go-mk-fetch-one.sh"

asset_list() {
    printf "go.mk\n"
    printf "golangci.yml\n"
    for script_name in ${GO_MK_SCRIPT_FILES:-}; do
        printf "%s\n" "${script_name}"
    done
    for module_name in ${GO_MK_MODULES:-}; do
        printf "%s\n" "${module_name}"
    done
}

update_assets() {
    local asset_name
    local destination_path

    mkdir -p .make
    while IFS= read -r asset_name; do
        if [[ -z "${asset_name}" ]]; then
            continue
        fi
        if [[ "${asset_name}" == "go.mk" ]]; then
            destination_path="${GO_MK:-go.mk}"
        else
            destination_path=".make/${asset_name}"
        fi
        bash "${FETCH_SCRIPT}" "${asset_name}" "${destination_path}" ""
        printf "updated: %s\n" "${asset_name}"
    done < <(asset_list)
}

smoke_fetch() {
    local asset_name
    local destination_path
    local count_output

    rm -rf .make
    mkdir -p .make
    while IFS= read -r asset_name; do
        if [[ -z "${asset_name}" ]]; then
            continue
        fi
        destination_path=".make/${asset_name}"
        bash "${FETCH_SCRIPT}" "${asset_name}" "${destination_path}" ""
    done < <(asset_list)
    count_output=$(find .make -maxdepth 1 -type f | wc -l | tr -d " ")
    printf "smoke-fetch: OK (%s assets fetched into .make/)\n" "${count_output}"
}

case "${1:-}" in
    update)
        update_assets
        ;;
    smoke-fetch)
        smoke_fetch
        ;;
    *)
        printf "go-mk-sync: unknown command %s\n" "${1:-}"
        exit 2
        ;;
esac
