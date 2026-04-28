# go-makefile

Shared Go build targets and reusable GitHub Actions workflows for all `agoodkind` Go projects.

## What's in here

| File | Purpose |
|------|---------|
| `go.mk` | Shared Makefile targets (see file for full list) |
| `golangci-template.yml` | Canonical golangci-lint config (projects extend this) |
| `templates/goreleaser.yaml.tmpl` | Canonical goreleaser template (bootstrap fills in binary name) |
| `bootstrap.sh` | One-time project setup script |
| `.github/workflows/_ci.yml` | Reusable CI workflow |
| `.github/workflows/_release.yml` | Reusable release workflow |

---

## Quickstart

Run once from the project root (requires `go.mod`):

```bash
curl -fsSL https://raw.githubusercontent.com/agoodkind/go-makefile/main/bootstrap.sh | bash
```

This creates:
- `Makefile`, with runtime-fetch `go.mk` bootstrap + project-specific targets
- `.golangci.yml`, extends the shared lint config
- `.goreleaser.yaml`, filled in with the inferred binary name
- `.gitignore` entry for `.make/`

Skips any file that already exists. Fails clearly if `go.mod` is missing.

---

## How it works

### `go.mk`

Fetched at runtime into `.make/go.mk`, never committed. Any `make` target on a fresh clone auto-bootstraps via curl with a `~/.cache/go-makefile/go.mk` fallback. Run `make sync` to force-update.

Run `make help` or read `go.mk` directly for the current target list. Default goal is `check` (full battery).

### `.golangci.yml`

Committed per-project. The bootstrap generates a minimal file that `extends` the canonical config:

```yaml
extends:
  - https://raw.githubusercontent.com/agoodkind/go-makefile/main/golangci-template.yml
```

Add project-specific overrides below the `extends` line.

### `.goreleaser.yaml`

Committed per-project. The bootstrap renders `templates/goreleaser.yaml.tmpl` to `.goreleaser.yaml` and substitutes the binary name. Edit as needed after bootstrap.

---

### `staticcheck-extra` (bundled AST analyzer set)

A small AST analyzer set ships in this repo at `staticcheck/`. It enforces
boundary logging, structured slog hygiene, and type discipline. Five
analyzers (all enabled by default):

| Flag | What it catches |
|---|---|
| `-missing_boundary_log` | `main()` functions missing a structured slog event |
| `-slog_error_without_err` | error-level slog calls without an `err` field |
| `-banned_direct_output` | `fmt.Print*`, stdlib `log.Print/Fatal/Panic` in production code |
| `-hot_loop_info_log` | Info-level slog inside `for`/`range` loops |
| `-no_any_or_empty_interface` | exported types/funcs using `any`/`interface{}` |

Default behaviour: pulled via `go install github.com/agoodkind/go-makefile/staticcheck/cmd/staticcheck-extra@latest`,
all 5 analyzers enabled. **Zero project Makefile setup required.**

Per-project overrides:

```makefile
# Pick a different analyzer subset (default enables all 5):
STATICCHECK_EXTRA_FLAGS := -slog_error_without_err -hot_loop_info_log

# Pin to a specific commit/tag/branch instead of @latest:
STATICCHECK_EXTRA_INSTALL := github.com/agoodkind/go-makefile/staticcheck/cmd/staticcheck-extra@v0.1.0

# Bring your own analyzer binary:
STATICCHECK_EXTRA_BIN := /usr/local/bin/my-analyzer

# Or build from a local fork on disk:
STATICCHECK_EXTRA_BUILD_REPO := $(HOME)/Sites/my-fork
STATICCHECK_EXTRA_BUILD_PKG  := ./cmd/my-analyzer

# Other knobs:
STATICCHECK_EXTRA_TARGETS  := ./...                           # default
STATICCHECK_EXTRA_BASELINE := .staticcheck-extra-baseline.txt # default
```

Targets:

| Target | Behaviour |
|---|---|
| `staticcheck-extra` | Runs analyzer, diffs vs baseline. **NEW** findings fail. **Resolved** findings just print a hint. |
| `staticcheck-extra-baseline` | Refresh the baseline file with current findings. Commit the baseline. |
| `staticcheck-extra-bin` | Internal. Resolves or builds the analyzer binary. |

Wired into `check` automatically. Passes silently when not configured.

Document each baseline entry in a `STATICCHECK-NOTES.md` so the next
person does not try to "fix" intentional exceptions.

---

## CI / releases

### Wire up CI

Create `.github/workflows/ci.yml`:

```yaml
name: CI

on: [push]

jobs:
  ci:
    uses: agoodkind/go-makefile/.github/workflows/_ci.yml@main
```

Runs the full check suite plus a GoReleaser config validation.

### Wire up releases

Create `.github/workflows/release.yml`:

```yaml
name: Release

on:
  push:
    branches: [main]

concurrency:
  group: release
  cancel-in-progress: true

jobs:
  release:
    uses: agoodkind/go-makefile/.github/workflows/_release.yml@main
    permissions:
      contents: write
    secrets: inherit
```

Every push to `main` creates a release tagged `YYYYMMDDHHmm-<hex-build>-<short-sha>`.
