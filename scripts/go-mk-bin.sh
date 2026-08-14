#!/usr/bin/env bash
set -euo pipefail

command_name="${1:-bin}"
root="${GO_MK_ROOT:-$PWD}"
output="${root}/.make/go-mk"

if [[ -n "${GO_MK_BIN:-}" ]]; then
    if [[ ! -x "${GO_MK_BIN}" ]]; then
        printf 'go-mk: %s not executable\n' "${GO_MK_BIN}" >&2
        exit 1
    fi
    exec "${GO_MK_BIN}" resolve-bin "${command_name}"
fi

if [[ -n "${GO_MK_BUILD_REPO:-}" ]] && [[ -d "${GO_MK_BUILD_REPO}/cmd/go-mk" ]]; then
    mkdir -p "${root}/.make"
    go build -C "${GO_MK_BUILD_REPO}" -o "${output}" "${GO_MK_BUILD_PKG:-./cmd/go-mk}"
    exec "${output}" resolve-bin "${command_name}"
fi

if [[ -x "${output}" ]]; then
    exec "${output}" resolve-bin "${command_name}"
fi

if [[ -n "${GO_MK_DEV_DIR:-}" ]] && [[ -d "${GO_MK_DEV_DIR}/cmd/go-mk" ]]; then
    mkdir -p "${root}/.make"
    go build -C "${GO_MK_DEV_DIR}" -o "${output}" "${GO_MK_BUILD_PKG:-./cmd/go-mk}"
    exec "${output}" resolve-bin "${command_name}"
fi

exec go run goodkind.io/go-makefile/cmd/go-mk@main resolve-bin "${command_name}"
