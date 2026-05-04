# go-makefile

Shared Go build targets and reusable GitHub Actions workflows for all `agoodkind` Go projects.

## What's in here

| File | Purpose |
| ---- | ------- |
| `go.mk` | Shared Makefile targets (see file for full list) |
| `golangci-template.yml` | Canonical golangci-lint v2 config (projects extend this) |
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

- `Makefile`, with parse-time `go.mk` fetch, cache fallback, and project identity variables
- `.golangci.yml`, extends the shared lint config
- `.goreleaser.yaml`, filled in with the inferred binary name
- `.gitignore` entry for `.make/`

Generated `Makefile` files stay intentionally small. They set `BINARY` and `CMD` for binary repos, include the shared `go.mk`, and otherwise inherit the canonical build, deploy, clean, lint, baseline, and check targets.

Skips any file that already exists. Fails clearly if `go.mod` is missing.

---

## How it works

### `go.mk`

Fetched into `.make/go.mk` before every Makefile parse, never committed. Every `make` invocation attempts to pull the latest remote `go.mk` with curl, updates `~/.cache/go-makefile/go.mk` on success, and falls back to the cached copy only when the remote is unavailable. Run `make update-go-mk` or `make go-mk-sync` to force the same refresh explicitly.

Run `make help` or read `go.mk` directly for the current target list. Default goal is `check` (full battery).

The shared lint flow is:

- `make lint-tools` installs `golangci-lint`, `gofumpt`, and `goimports`
- `make lint-golangci` runs `golangci-lint run ./...`, diffs findings against `.golangci-lint-baseline.txt`, and fails only on new findings
- `make lint-golangci-baseline` refreshes `.golangci-lint-baseline.txt` with current findings and `first_added` / `last_seen` timestamps
- `make lint` runs baseline-gated `golangci-lint`, the configured GolangCI formatters in diff mode, `go tool gocyclo -over $(GOCYCLO_OVER) $(GOCYCLO_TARGETS)`, and `staticcheck-extra`
- `make fmt` applies the configured GolangCI formatters
- `make build-check` runs the full non-test quality gate: `vet`, `lint`, and `govulncheck`
- `make build` runs `build-check`, then `$(GO_BUILD_ENV) $(GO) build $(GO_BUILD_OUTPUT_FLAGS) $(GO_BUILD_FLAGS) $(GO_BUILD_LDFLAGS_FLAGS) $(GO_BUILD_TARGETS)`
- `make deploy` runs `$(GO_DEPLOY_COMMAND)` when set, otherwise `$(GO_INSTALL_ENV) $(GO) install $(GO_INSTALL_FLAGS) $(GO_INSTALL_LDFLAGS_FLAGS) $(GO_INSTALL_TARGET)` and requires `GO_INSTALL_TARGET` or `CMD`
- `make install-binary` runs `build`, then installs `$(GO_INSTALL_BIN_SOURCE)` into `$(DESTDIR)$(GO_INSTALL_BIN_DIR)/$(GO_INSTALL_BIN_NAME)`
- `make clean` removes `$(BINARY)` when `BINARY` is set
- `make check` runs `build`, then `test`

The shared build flow is configured through variables instead of project-local target overrides:

```makefile
BINARY               := mycmd                         # optional; used by GO_BUILD_OUTPUT and clean
CMD                  := ./cmd/mycmd                   # optional; sets default build/install target
GO                   := go                            # default Go command
GO_ENV               := CGO_ENABLED=1                 # optional env prefix for Go commands
GO_BUILD_ENV         := $(GO_ENV)                     # defaults to GO_ENV
GO_BUILD_OUTPUT      := $(BINARY)                     # default when CMD is set; set empty to omit -o
GO_BUILD_OUTPUT_FLAGS := -o $(GO_BUILD_OUTPUT)        # default when GO_BUILD_OUTPUT is set
GO_BUILD_FLAGS       := -tags fdb                     # optional; used by build and install
GO_BUILD_LDFLAGS     := -s -w                         # optional; converted to -ldflags "..."
GO_BUILD_TARGETS     := ./cmd/server                  # default: $(CMD), else ./...
GO_TEST_ENV          := $(GO_ENV)                     # defaults to GO_ENV
GO_TEST_TARGETS      := ./...                         # default
GO_VET_ENV           := $(GO_ENV)                     # defaults to GO_ENV
GO_VET_TARGETS       := ./...                         # default
GOVULNCHECK_ENV      := $(GO_ENV)                     # defaults to GO_ENV
GOVULNCHECK_TARGETS  := ./...                         # default
GO_INSTALL_FLAGS     := $(filter-out -o %,$(GO_BUILD_FLAGS)) # default
GO_INSTALL_LDFLAGS   := $(GO_BUILD_LDFLAGS)           # optional; converted to -ldflags "..."
GO_INSTALL_TARGET    := $(CMD)                        # default
GO_DEPLOY_COMMAND    := ./scripts/deploy.sh           # optional; deploy delegates when set
GO_DEPLOY_TARGETS    := preflight backup              # optional deploy prerequisites
GO_DEPLOY_INSTALL    := true                          # default; set false when command owns deploy
GO_INSTALL_BIN_DIR   := /opt/scripts                  # enables install-binary
GO_INSTALL_BIN_SOURCE := $(GO_BUILD_OUTPUT)           # default
GO_INSTALL_BIN_NAME  := $(BINARY)                     # default
GO_INSTALL_BIN_MODE  := 0755                          # default
BUILD_CHECKS         := true                          # default; build depends on build-check
```

### `.golangci.yml`

Committed per-project. The bootstrap generates a minimal file that `extends` the canonical config:

```yaml
extends:
  - https://raw.githubusercontent.com/agoodkind/go-makefile/main/golangci-template.yml
```

The shared template uses GolangCI-Lint v2 `linters.default: all`, then narrows behavior with explicit disables, exclusions, formatter settings, `cyclop` complexity capped at 50, strict `nolintlint` requirements, and exported symbol doc checks so intentionally noisy style rules stay opt-out by default while comments must remain useful and explained. Add project-specific overrides below the `extends` line.

### `golangci-lint` baseline

The shared `lint-golangci` target runs `golangci-lint run` through the same baseline-diff gate used by Clyde. Existing findings live in `.golangci-lint-baseline.txt`; new findings fail the target, and resolved findings print without failing.

Per-project overrides:

```makefile
GOLANGCI_LINT_TARGETS  := ./...                         # default
GOLANGCI_LINT_FLAGS    := -c .golangci.yml              # optional extra run flags
GOLANGCI_LINT_BASELINE := .golangci-lint-baseline.txt   # default
GOLANGCI_LINT_BASELINE_RUNS := 3                             # default
```

Targets:

| Target | Behaviour |
| ------ | --------- |
| `lint-golangci` | Runs `golangci-lint`, diffs normalized findings against `.golangci-lint-baseline.txt`, and fails on new findings. |
| `lint-golangci-baseline` | Refreshes `.golangci-lint-baseline.txt` with current findings sampled across `$(GOLANGCI_LINT_BASELINE_RUNS)` runs and writes `first_added` and `last_seen` UTC timestamps for each finding. |

Commit the baseline only when the remaining findings are intentional. Refresh it after fixing old findings so the baseline continues to describe the current tree.

### `.goreleaser.yaml`

Committed per-project. The bootstrap renders `templates/goreleaser.yaml.tmpl` to `.goreleaser.yaml` and substitutes the binary name. Edit as needed after bootstrap.

---

### `staticcheck-extra` (bundled AST analyzer set)

A small AST analyzer set ships in this repo at `staticcheck/`. It enforces boundary logging, structured slog hygiene, and type discipline. Seventeen analyzers are enabled by default:

| Flag | What it catches |
| ---- | --------------- |
| `-slog_error_without_err` | Error-level slog calls without an `err` field |
| `-banned_direct_output` | `fmt.Print*`, stdlib `log.Print/Fatal/Panic` in production code |
| `-hot_loop_info_log` | Info-level slog inside `for`/`range` loops |
| `-missing_boundary_log` | `main()` functions missing a structured slog event |
| `-no_any_or_empty_interface` | Exported types and funcs using `any` or `interface{}` |
| `-wrapped_error_without_slog` | Wrapped errors returned without a nearby structured error log |
| `-os_exit_outside_main` | `os.Exit` calls outside `main()` |
| `-context_todo_in_production` | `context.TODO()` in non-test, non-generated code |
| `-time_sleep_in_production` | `time.Sleep` in non-test, non-generated code |
| `-panic_in_production` | `panic` in non-test, non-generated code |
| `-time_now_outside_clock` | `time.Now()` outside accepted clock boundaries |
| `-goroutine_without_recover` | Goroutines launched without a recovery wrapper |
| `-silent_defer_close` | Deferred `Close()` calls that discard errors silently |
| `-slog_missing_trace_id` | Structured logs missing trace identifiers |
| `-grpc_handler_missing_peer_enrichment` | gRPC handlers that do not enrich logs with peer details |
| `-sensitive_field_in_log` | Structured logs that include fields likely to carry secrets |
| `-nolint_ban` | Any `//nolint` comment in production code |

Default behaviour: pulled via `go install github.com/agoodkind/go-makefile/staticcheck/cmd/staticcheck-extra@latest`, with all bundled analyzers enabled by default. Zero project Makefile setup is required.

Per-project overrides:

```makefile
# Pick a smaller analyzer subset:
STATICCHECK_EXTRA_FLAGS := -slog_error_without_err -hot_loop_info_log

# Restore the canonical full analyzer set explicitly:
STATICCHECK_EXTRA_FLAGS = $(STATICCHECK_EXTRA_CORE_FLAGS) $(STATICCHECK_EXTRA_STRICT_FLAGS)

# Pin to a specific commit, tag, or branch instead of @latest:
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
| ------ | --------- |
| `staticcheck-extra` | Runs the custom analyzer set, diffs vs baseline, and fails on new findings. Resolved findings print without failing. |
| `staticcheck-extra-baseline` | Refreshes `.staticcheck-extra-baseline.txt` with current findings and writes `first_added` and `last_seen` UTC timestamps for each finding. Commit the baseline only when remaining findings are intentional. |
| `staticcheck-extra-bin` | Internal. Resolves or builds the analyzer binary. |

`make lint` and `make check` both include `staticcheck-extra` automatically.

The baseline starts with a generated-at header, and each baseline entry keeps the analyzer output before a tab-separated metadata suffix, for example:

```text
# staticcheck-extra: generated_at=2026-05-03T18:30:00Z
path/to/file.go:10:2: message<TAB># staticcheck-extra:first_added=2026-05-03T18:30:00Z last_seen=2026-05-03T18:30:00Z
```

The `staticcheck-extra` gate compares only the analyzer output portion, so refreshing `last_seen` does not create a new finding.

Document each baseline entry in a `STATICCHECK-NOTES.md` so the next person does not try to “fix” an intentional exception. When findings are resolved, refresh the baseline with `make staticcheck-extra-baseline` and commit the updated file.

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

The reusable workflow runs a dedicated lint job, build and test, `go vet`, `govulncheck`, and a GoReleaser config validation.

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

The release template signs and notarizes macOS binaries through GoReleaser when Apple secrets are present. Configure `APPLE_DEVELOPER_ID_CERT`, `APPLE_DEVELOPER_ID_CERT_PASSWORD`, `APPLE_NOTARYTOOL_ISSUER_ID`, `APPLE_NOTARYTOOL_KEY_ID`, and `APPLE_NOTARYTOOL_KEY` as GitHub Actions secrets for CI. Copy `notarize.env.example` to `notarize.env` for local `make release` runs.
