#!/usr/bin/env bash
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
    printf '%s\n' "error: go is required to run go-makefile bootstrap" >&2
    exit 1
fi

exec go run goodkind.io/go-makefile/cmd/go-mk@main bootstrap "$@"
