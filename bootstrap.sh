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
	local raw_url="${BASE_URL}/${rel}"
	local api_url="https://api.github.com/repos/agoodkind/go-makefile/contents/${rel}?ref=main"
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
	# Try GitHub Contents API first to bypass the raw-content CDN (which can
	# serve stale bytes for several minutes). Fall back to cache-busted raw,
	# then plain raw.
	if curl -fsSL -H "Accept: application/vnd.github.raw" --connect-timeout 5 --max-time 10 "$api_url" -o "${dest}.new" \
		|| curl -fsSL --connect-timeout 5 --max-time 10 "${raw_url}?v=$(date +%s)" -o "${dest}.new" \
		|| curl -fsSL --connect-timeout 5 --max-time 10 "$raw_url" -o "${dest}.new"; then
		mv "${dest}.new" "$dest"
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

# VPKG points at the project's version package. We emit it only when an
# internal/version directory already exists, so newly scaffolded tiny binaries
# do not get a stamping flag pointing at a package they have not written yet.
# Library layouts never get VPKG (they are stamped by their binary consumers).
VPKG=""
if [ "$LAYOUT" = "binary" ] && [ -d "internal/version" ]; then
	VPKG="$MODULE/internal/version"
fi

CTX_JSON=$(printf '{"Binary":"%s","Cmd":"%s","Layout":"%s","Vpkg":"%s","BaseURL":"%s"}\n' "$BINARY" "$CMD" "$LAYOUT" "$VPKG" "$BASE_URL")

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
# .golangci.yml is intentionally NOT rendered into the project. The central
# golangci config is fetched at runtime by go.mk into .make/golangci.yml and
# is wired into GOLANGCI_LINT_FLAGS automatically. A per-repo .golangci.yml
# would split the config between project-local and central and let agents
# weaken rules without going through go-makefile. If a project genuinely
# needs an override, do it in go-makefile/golangci.yml so every consumer
# picks it up. If a stale per-repo .golangci.yml exists from before the
# centralization, warn so the developer removes it.
# ---------------------------------------------------------------------------
if [ -f .golangci.yml ]; then
	warn ".golangci.yml exists in project root. The central go-makefile/golangci.yml fetched into .make/golangci.yml is the canonical config; the per-repo file is ignored by GOLANGCI_LINT_FLAGS. Remove it or move overrides upstream into go-makefile."
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

echo ""
echo "done."
echo ""
echo "Lint and build are centralized in go-makefile. The canonical entry points are:"
echo ""
echo "  make build   vet + full lint chain + govulncheck, then go build"
echo "  make check   build + test"
echo "  make lint    just the full lint chain"
echo "  make fmt     apply gofumpt + goimports"
echo ""
echo "Run 'make help' for the full target list, including per-linter sub-targets"
echo "and baseline-refresh targets. Do not add project-local lint, deadcode, audit,"
echo "or staticcheck targets; doing so splits enforcement and lets agents bypass"
echo "the central rules."
