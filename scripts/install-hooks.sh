#!/usr/bin/env bash
# install-hooks.sh: idempotently link this repo's git pre-commit hook to
# the canonical hook in go-makefile. Re-running is safe; the link is only
# replaced if it doesn't already point at the canonical script.
#
# Run from the consumer repo root:
#   bash <(curl -fsSL https://raw.githubusercontent.com/agoodkind/go-makefile/main/scripts/install-hooks.sh)
# or, with go-makefile checked out locally:
#   bash $GO_MK_DEV_DIR/scripts/install-hooks.sh

set -eu

# Resolve the canonical hook source. Prefer GO_MK_DEV_DIR for local dev;
# else fetch via gh api (authenticated) into a per-user shared location;
# else fall back to raw URL. The script itself is idempotent so repeated
# runs converge on the same final symlink.
HOOKS_DEST_DIR="${GO_MK_HOOKS_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/go-makefile/hooks}"
HOOK_NAME="pre-commit"

resolve_canonical_hook() {
    if [ -n "${GO_MK_DEV_DIR:-}" ] && [ -f "$GO_MK_DEV_DIR/hooks/$HOOK_NAME" ]; then
        printf '%s\n' "$GO_MK_DEV_DIR/hooks/$HOOK_NAME"
        return 0
    fi
    mkdir -p "$HOOKS_DEST_DIR"
    local target="$HOOKS_DEST_DIR/$HOOK_NAME"
    if command -v gh >/dev/null 2>&1 && gh api "repos/agoodkind/go-makefile/contents/hooks/$HOOK_NAME?ref=main" -H "Accept: application/vnd.github.raw" > "$target" 2>/dev/null && [ -s "$target" ]; then
        chmod +x "$target"
        printf '%s\n' "$target"
        return 0
    fi
    if curl -fsSL --connect-timeout 5 --max-time 10 \
            "https://raw.githubusercontent.com/agoodkind/go-makefile/main/hooks/$HOOK_NAME" \
            -o "$target" 2>/dev/null && [ -s "$target" ]; then
        chmod +x "$target"
        printf '%s\n' "$target"
        return 0
    fi
    printf 'install-hooks: cannot fetch canonical hook (no dev dir, no gh auth, no network)\n' >&2
    return 1
}

git_dir=$(git rev-parse --git-dir 2>/dev/null) || {
    printf 'install-hooks: not in a git repo\n' >&2
    exit 1
}

canonical=$(resolve_canonical_hook)
hook_link="$git_dir/hooks/$HOOK_NAME"
mkdir -p "$git_dir/hooks"

# Idempotent: only act when the existing link is wrong or missing.
if [ -L "$hook_link" ] && [ "$(readlink "$hook_link")" = "$canonical" ]; then
    printf 'install-hooks: %s already linked to %s\n' "$hook_link" "$canonical"
    exit 0
fi

# If a non-symlink hook exists (custom or stale), back it up before overwriting.
if [ -e "$hook_link" ] && [ ! -L "$hook_link" ]; then
    backup="$hook_link.backup.$(date +%s)"
    mv "$hook_link" "$backup"
    printf 'install-hooks: existing hook moved to %s\n' "$backup" >&2
fi

ln -sfn "$canonical" "$hook_link"
printf 'install-hooks: linked %s -> %s\n' "$hook_link" "$canonical"
