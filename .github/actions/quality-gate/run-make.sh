#!/usr/bin/env bash
set -euo pipefail

make_command=(make)
if [[ -n "$GO_MK_ACTION_MAKEFILE" ]]; then
    make_command+=(-f "$GO_MK_ACTION_MAKEFILE")
fi

if [[ -z "$GO_MK_ACTION_COMPOSE_SERVICE" ]]; then
    exec "${make_command[@]}" "$@"
fi

exec docker compose run --rm --no-deps --pull never -T \
    --volume "${GITHUB_OUTPUT:?GitHub must provide a step output file}:/tmp/go-mk-step-output" \
    --env GITHUB_OUTPUT=/tmp/go-mk-step-output \
    "$GO_MK_ACTION_COMPOSE_SERVICE" "${make_command[@]}" "$@"
