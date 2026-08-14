#!/usr/bin/env bash
set -eu

HOOKS_DEST_DIR="${GO_MK_HOOKS_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/go-makefile/hooks}"
HOOK_NAME="pre-commit"

resolve_canonical_hook() {
    if [ -n "${GO_MK_DEV_DIR:-}" ] && [ -f "$GO_MK_DEV_DIR/hooks/$HOOK_NAME" ]; then
        printf '%s\n' "$GO_MK_DEV_DIR/hooks/$HOOK_NAME"
        return 0
    fi
    if [ -f ".make/hooks/$HOOK_NAME" ]; then
        printf '%s\n' "$(cd .make/hooks && pwd)/$HOOK_NAME"
        return 0
    fi
    mkdir -p "$HOOKS_DEST_DIR"
    local target="$HOOKS_DEST_DIR/$HOOK_NAME"
    if curl -fsSL --connect-timeout 5 --max-time 10 \
            "https://raw.githubusercontent.com/agoodkind/go-makefile/main/hooks/$HOOK_NAME" \
            -o "$target" 2>/dev/null && [ -s "$target" ]; then
        chmod +x "$target"
        printf '%s\n' "$target"
        return 0
    fi
    printf 'install-hooks: cannot fetch canonical hook (no dev dir, no provisioned hook, no network)\n' >&2
    return 1
}

git_dir=$(git rev-parse --git-dir 2>/dev/null) || {
    printf 'install-hooks: not in a git repo\n' >&2
    exit 1
}

canonical=$(resolve_canonical_hook)
hook_link="$git_dir/hooks/$HOOK_NAME"
mkdir -p "$git_dir/hooks"
ln -sf "$canonical" "$hook_link"
chmod +x "$canonical"
printf 'install-hooks: linked %s -> %s\n' "$hook_link" "$canonical"
