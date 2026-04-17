#!/usr/bin/env bash
set -euo pipefail

BASE_URL="https://raw.githubusercontent.com/agoodkind/go-makefile/main"

warn() { echo "warning: $*" >&2; }
die() {
	echo "error: $*" >&2
	exit 1
}
skip() { echo "skipping $1 (already exists)"; }

usage() {
	cat <<EOF
bootstrap.sh scaffolds a Go project against go-makefile.

Usage:
    bootstrap.sh [options]

Options:
    --module=<path>     Module path for 'go mod init' when no go.mod exists.
                        Example: --module=goodkind.io/myproj
                        Env fallback: GO_MODULE_PATH

    --vanity=<root>     Vanity path root used when inferring from directory name.
                        Example: --vanity=goodkind.io
                        Env fallback: GO_VANITY_ROOT (default: goodkind.io)

    --library           Force library layout. No cmd, no goreleaser, no
                        release workflow, no build/deploy/clean targets.

    --binary            Force binary layout (standard output).

    --yes               Accept inferred values without prompting. Useful for
                        non-interactive use like 'curl ... | bash'.

    --help              Show this message.

Behavior without flags:
    If go.mod is absent, the module path is inferred in this order.
        1. --module or GO_MODULE_PATH.
        2. git remote 'origin' normalized to host/user/repo.
        3. <vanity-root>/<basename-of-cwd>.
    If stdin is a TTY, the inferred value is confirmed via prompt.
    If stdin is not a TTY and no explicit value was provided, bootstrap
    errors out rather than guessing silently.

    Library vs binary is detected automatically. A cmd/ directory with at
    least one subdirectory means binary. Otherwise library.
EOF
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
CLI_MODULE=""
CLI_VANITY=""
CLI_LAYOUT=""
CLI_YES="false"

for arg in "$@"; do
	case "$arg" in
	--module=*) CLI_MODULE="${arg#--module=}" ;;
	--vanity=*) CLI_VANITY="${arg#--vanity=}" ;;
	--library) CLI_LAYOUT="library" ;;
	--binary) CLI_LAYOUT="binary" ;;
	--yes | -y) CLI_YES="true" ;;
	--help | -h)
		usage
		exit 0
		;;
	*) die "unknown argument: $arg (try --help)" ;;
	esac
done

VANITY_ROOT="${CLI_VANITY:-${GO_VANITY_ROOT:-goodkind.io}}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Normalize a git remote URL to a module path shaped string.
# git@github.com:agoodkind/foo.git      becomes github.com/agoodkind/foo
# https://github.com/agoodkind/foo.git  becomes github.com/agoodkind/foo
# https://github.com/agoodkind/foo      becomes github.com/agoodkind/foo
normalize_git_remote() {
	local url="$1"
	local path=""
	case "$url" in
	git@*:*)
		path="${url#git@}"
		path="${path/:/\/}"
		;;
	http://* | https://* | ssh://*)
		path="${url#*://}"
		path="${path#*@}"
		;;
	*)
		echo ""
		return
		;;
	esac
	path="${path%.git}"
	path="${path%/}"
	echo "$path"
}

# Infer a module path. Prints the inferred value, or empty if none.
infer_module_path() {
	local basename_cwd
	basename_cwd=$(basename "$PWD")

	if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		local remote
		remote=$(git config --get remote.origin.url || true)
		if [ -n "$remote" ]; then
			local normalized
			normalized=$(normalize_git_remote "$remote")
			if [ -n "$normalized" ]; then
				echo "$normalized"
				return
			fi
		fi
	fi

	if [ -n "$VANITY_ROOT" ] && [ -n "$basename_cwd" ] && [ "$basename_cwd" != "/" ]; then
		echo "$VANITY_ROOT/$basename_cwd"
		return
	fi

	echo ""
}

# Prompt with a default value. Writes the answer to stdout.
# Falls back to the default if stdin is not a TTY and --yes was passed.
prompt_with_default() {
	local question="$1"
	local default="$2"

	if [ "$CLI_YES" = "true" ]; then
		echo "$default"
		return
	fi

	if [ ! -t 0 ]; then
		echo "$default"
		return
	fi

	local answer=""
	read -r -p "$question [$default]: " answer </dev/tty
	if [ -z "$answer" ]; then
		echo "$default"
	else
		echo "$answer"
	fi
}

# ---------------------------------------------------------------------------
# Resolve or create go.mod
# ---------------------------------------------------------------------------
if [ ! -f go.mod ]; then
	RESOLVED_MODULE="${CLI_MODULE:-${GO_MODULE_PATH:-}}"

	if [ -z "$RESOLVED_MODULE" ]; then
		INFERRED=$(infer_module_path)
		if [ -z "$INFERRED" ]; then
			die "no go.mod found and could not infer a module path. Pass --module=<path> or set GO_MODULE_PATH."
		fi

		if [ -t 0 ] && [ "$CLI_YES" != "true" ]; then
			RESOLVED_MODULE=$(prompt_with_default "module path" "$INFERRED")
		else
			if [ "$CLI_YES" != "true" ]; then
				die "no go.mod and stdin is not a TTY. Pass --module=<path>, set GO_MODULE_PATH, or add --yes to accept inferred value: $INFERRED"
			fi
			RESOLVED_MODULE="$INFERRED"
		fi
	fi

	[ -n "$RESOLVED_MODULE" ] || die "module path is empty"
	echo "running: go mod init $RESOLVED_MODULE"
	go mod init "$RESOLVED_MODULE"
fi

MODULE=$(awk '/^module / { print $2; exit }' go.mod)
[ -n "$MODULE" ] || die "could not read module from go.mod"

# ---------------------------------------------------------------------------
# Detect library vs binary layout
# ---------------------------------------------------------------------------
LAYOUT="$CLI_LAYOUT"

if [ -z "$LAYOUT" ]; then
	if [ -d "cmd" ] && [ "$(find cmd -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')" -ge 1 ]; then
		LAYOUT="binary"
	else
		LAYOUT="library"
	fi
fi

# Binary name and cmd path only matter when LAYOUT is binary.
BINARY=""
CMD=""
if [ "$LAYOUT" = "binary" ]; then
	BINARY="${MODULE##*/}"
	if [ -d "cmd/$BINARY" ]; then
		CMD="./cmd/$BINARY"
	elif [ -d "cmd" ] && [ "$(find cmd -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')" -eq 1 ]; then
		ONLY=$(find cmd -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | head -n1)
		CMD="./cmd/$ONLY"
		BINARY="$ONLY"
	else
		CMD="./cmd/$BINARY"
	fi
fi

echo "module:  $MODULE"
echo "layout:  $LAYOUT"
if [ "$LAYOUT" = "binary" ]; then
	echo "binary:  $BINARY"
	echo "cmd:     $CMD"
fi
echo ""

# ---------------------------------------------------------------------------
# Template render (render.go and templates from BASE_URL)
# ---------------------------------------------------------------------------
CACHE_ROOT="${HOME}/.cache/go-makefile"

file_mtime() {
	local f="$1"
	local m
	if m=$(stat -c %Y "$f" 2>/dev/null); then
		echo "$m"
		return
	fi
	stat -f %m "$f"
}

ensure_cached_asset() {
	local rel="$1"
	local url="${BASE_URL}/${rel}"
	local dest="${CACHE_ROOT}/${rel}"
	mkdir -p "$(dirname "$dest")"
	local now
	now=$(date +%s)
	if [ -f "$dest" ]; then
		local age
		age=$((now - $(file_mtime "$dest")))
		if [ "$age" -lt 86400 ]; then
			echo "$dest"
			return
		fi
	fi
	if curl -fsSL --connect-timeout 5 --max-time 10 "$url" -o "${dest}.new" && mv "${dest}.new" "$dest"; then
		echo "$dest"
		return
	fi
	rm -f "${dest}.new"
	if [ -f "$dest" ]; then
		warn "${rel} fetch failed, using cached version"
		echo "$dest"
		return
	fi
	die "${rel} fetch failed and no cache available"
}

CTX_JSON=$(printf '{"Binary":"%s","Cmd":"%s","Layout":"%s","BaseURL":"%s"}\n' "$BINARY" "$CMD" "$LAYOUT" "$BASE_URL")

render_artifact() {
	local rel="$1"
	local out="$2"
	local tmpl_path
	local render_path
	tmpl_path=$(ensure_cached_asset "$rel")
	render_path=$(ensure_cached_asset "render.go")
	printf '%s' "$CTX_JSON" | go run "$render_path" "$tmpl_path" >"$out"
}

# ---------------------------------------------------------------------------
# Makefile
# ---------------------------------------------------------------------------
if [ -f Makefile ]; then
	skip Makefile
else
	render_artifact "templates/Makefile.tmpl" Makefile
	echo "created Makefile ($LAYOUT)"
fi

# ---------------------------------------------------------------------------
# .golangci.yml
# ---------------------------------------------------------------------------
if [ -f .golangci.yml ]; then
	skip .golangci.yml
else
	render_artifact "templates/golangci.yml.tmpl" .golangci.yml
	echo "created .golangci.yml"
fi

# ---------------------------------------------------------------------------
# .goreleaser.yaml (binary layout only)
# ---------------------------------------------------------------------------
if [ "$LAYOUT" = "binary" ]; then
	if [ -f .goreleaser.yaml ]; then
		skip .goreleaser.yaml
	else
		render_artifact "templates/goreleaser.yaml.tmpl" .goreleaser.yaml
		echo "created .goreleaser.yaml"
	fi
fi

# ---------------------------------------------------------------------------
# .gitignore
# ---------------------------------------------------------------------------
if [ -f .gitignore ]; then
	if ! grep -q "^\.make/" .gitignore; then
		echo ".make/" >>.gitignore
		echo "added .make/ to .gitignore"
	fi
else
	echo ".make/" >.gitignore
	echo "created .gitignore"
fi

# ---------------------------------------------------------------------------
# GitHub workflows
# ---------------------------------------------------------------------------
if [ -f .github/workflows/ci.yml ]; then
	skip .github/workflows/ci.yml
else
	mkdir -p .github/workflows
	render_artifact "templates/ci.yml.tmpl" .github/workflows/ci.yml
	echo "created .github/workflows/ci.yml"
fi

# Release workflow only for binary layout. Libraries are tagged, not released.
if [ "$LAYOUT" = "binary" ]; then
	if [ -f .github/workflows/release.yml ]; then
		skip .github/workflows/release.yml
	else
		mkdir -p .github/workflows
		render_artifact "templates/release.yml.tmpl" .github/workflows/release.yml
		echo "created .github/workflows/release.yml"
	fi
fi

echo ""
echo "done. next: make"
