#!/usr/bin/env bash
set -eo pipefail

GO_MK_BIN="${GO_MK_BIN:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/go-mk}"
if [[ ! -x "${GO_MK_BIN}" ]]; then
    GO_MK_BIN=".make/go-mk"
fi
if [[ -x "${GO_MK_BIN}" ]]; then
    exec "${GO_MK_BIN}" provision
fi
exec go run goodkind.io/go-makefile/cmd/go-mk@main provision
