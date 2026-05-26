# go-makefile

Shared Go build targets and reusable GitHub Actions workflows for all `agoodkind` Go projects.

## What's in here

| File | Purpose |
| ---- | ------- |
| `go.mk` | Shared Makefile targets (see file for full list) |
| `scripts/go-mk-*.sh` and `scripts/go-mk-*.awk` | Runtime helper scripts used by `go.mk` |
| `golangci.yml` | Canonical golangci-lint v2 config |
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
- `.goreleaser.yaml`, filled in with the inferred binary name
- `.gitignore` entry for `.make/`

Generated `Makefile` files stay intentionally small. They set `BINARY` and `CMD` for binary repos, include the shared `go.mk`, and otherwise inherit the canonical build, deploy, clean, lint, and check targets.

Skips any file that already exists. Fails clearly if `go.mod` is missing.

---

## How it works

### `go.mk`

Fetched into `.make/go.mk` before every Makefile parse, never committed. `go.mk` then fetches its helper scripts, sibling modules, and `golangci.yml` into `.make/`. Run `make update-go-mk` or `make go-mk-sync` to force the same refresh explicitly.

Run `make help` or read `go.mk` directly for the current target list. Default goal is `check` (full battery).

The shared lint flow is:

- `make lint-tools` installs `golangci-lint`, `gofumpt`, and `goimports`
- `make lint-golangci` runs `golangci-lint run ./...`, diffs findings against `.golangci-lint-baseline.txt`, and fails only on new findings
- `make lint` runs baseline-aware `golangci-lint`, the configured GolangCI formatters in diff mode, baseline-aware `gocyclo`, deadcode, and `staticcheck-extra`
- `make fmt` applies the configured GolangCI formatters
- `make build-check` runs the full non-test quality gate: `vet`, `lint`, and `govulncheck`
- `make build` runs `build-check`, then `$(GO_BUILD_ENV) $(GO) build $(GO_BUILD_OUTPUT_FLAGS) $(GO_BUILD_FLAGS) $(GO_BUILD_LDFLAGS_FLAGS) $(GO_BUILD_TARGETS)`
- `make deploy` runs `$(GO_DEPLOY_COMMAND)` when set, otherwise `$(GO_INSTALL_ENV) $(GO) install $(GO_INSTALL_FLAGS) $(GO_INSTALL_LDFLAGS_FLAGS) $(GO_INSTALL_TARGET)` and requires `GO_INSTALL_TARGET` or `CMD`
- `make install-binary` runs `build`, then installs `$(GO_INSTALL_BIN_SOURCE)` into `$(DESTDIR)$(GO_INSTALL_BIN_DIR)/$(GO_INSTALL_BIN_NAME)`
- `make clean` removes `$(BINARY)` when `BINARY` is set
- `make check` runs the lint gate. Run `make build` and `make test` when you need build and test signals.

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

### GolangCI-Lint config

The canonical config lives in this repo at `golangci.yml`. Consumer projects use the central config that `go.mk` fetches into `.make/golangci.yml` at runtime, so bootstrap does not generate a per-project `.golangci.yml`.

The shared config uses GolangCI-Lint v2 `linters.default: all`, then narrows behavior with explicit disables, exclusions, formatter settings, `cyclop` complexity capped at 50, strict `nolintlint` requirements, and exported symbol doc checks so intentionally noisy style rules stay opt-out by default while comments must remain useful and explained. Shared lint gates exclude Go test files by default.

### `golangci-lint` baseline

The shared `lint-golangci` target runs `golangci-lint run` through the same baseline-diff gate used by Clyde. Existing findings live in `.golangci-lint-baseline.txt`; new findings fail the target, and resolved findings print without failing.

Per-project overrides:

```makefile
GOLANGCI_LINT_TARGETS  := ./...                         # default
GOLANGCI_LINT_FLAGS    := -c .golangci.yml              # optional extra run flags
GOLANGCI_LINT_BASELINE := .golangci-lint-baseline.txt   # default
GOLANGCI_LINT_BASELINE_RUNS := 3                             # default
GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS := _test\.go:                 # built-in
GOLANGCI_LINT_EXCLUDE_PATHS := gen/:,third_party/:                # optional extra grep -E patterns
```

Targets:

| Target | Behaviour |
| ------ | --------- |
| `lint-golangci` | Runs `golangci-lint`, diffs normalized findings against `.golangci-lint-baseline.txt`, and fails on new findings. |
| `lint-golangci-baseline` | Syncs the baseline to the current finding set. |
| `lint-golangci-baseline-prune-fixed` | Removes fixed findings from the baseline without accepting new findings. |
| `lint-golangci-baseline-accept-new` | Accepts new findings into the baseline while keeping fixed findings saved. |
| `lint-golangci-scope LINTER=.. RULE=..` | Runs and gates one linter or rule against only its slice of the baseline. |
| `lint-golangci-baseline-scope LINTER=.. RULE=..` | Baselines only that slice; every other linter's saved rows stay byte-for-byte unchanged. |
| `lint-golangci-baseline-scope-accept-new LINTER=.. RULE=..` | Accepts new findings for that slice only. |

Each golangci linter is independently runnable and baselinable through the scope knobs. `LINTER=<name>` selects a whole linter by its trailing `(name)` tag and runs it with `--enable-only` for speed. `RULE=<name>` selects a meta-linter sub-rule by its `name:` message prefix, for example a revive rule, and binds to the linter tag when `LINTER` is also set. `GOLANGCI_LINT_BASELINE_SCOPE_PATTERN` overrides both with a literal grep-compatible regex. A scoped baseline target refuses to run when no scope is set, so it cannot silently sync the whole baseline. Example: `BASELINE_CONFIRM=1 BASELINE_TOKEN="$(...)" make lint-golangci-baseline-scope RULE=file-length-limit`.

The revive `file-length-limit` rule (max 1000 lines per file) ships enabled in the shared `golangci.yml`. Its findings carry no column and the reported line number is the file's length, so a baselined oversized file stays matched only while its length is unchanged. Adding or removing lines re-triggers the gate, which is an intentional "do not grow already-oversized files" policy and differs from the line-independent matching used by `gocyclo`.

Baseline mutation targets are protected by the generic token gate. Set `BASELINE_CONFIRM=1` and `BASELINE_TOKEN` to the slugified output of `BASELINE_TOKEN_CMD` to permit a mutation. `baseline`, `baseline-prune-fixed`, and `baseline-accept-new` apply the same modes to every baseline. The `*-baseline-remove-fixed` and `baseline-remove-fixed` targets are aliases for `*-baseline-prune-fixed` and `baseline-prune-fixed`; `baseline-add-new` is an alias for `baseline-accept-new`.

### Update notices and auto-baseline

When go-makefile introduces a new gate or rule, `notices.txt` carries a record describing it, and `scripts/go-mk-notice.sh` runs as a prerequisite of `lint`, `check`, and `build`. On the first build that sees an unapplied notice with an auto-baseline directive, the notice grandfathers only that new rule's existing findings into the golangci baseline through the scoped path, then asks you to review the diff and commit it. The build does not fail on the new rule's pre-existing findings, and genuine new violations of other gates still fail normally, because the auto-baseline only ever writes the declared slice.

Applied notices are recorded in the committed `.go-mk-applied-notices` file, one id per line. Commit it alongside the baseline it produced. The committed record is what stops a fresh checkout or CI run from re-grandfathering violations that were added after the first rollout. The gitignored `.make/.go-mk-notice-seen` only dedupes the printed summary and is safe to discard.

### `gocyclo` baseline

The shared `lint-gocyclo` target runs `gocyclo`, normalizes each cyclomatic-complexity finding into the shared `file:line:column: message` format, diffs findings against `.gocyclo-baseline.txt`, and fails only on new findings.

Per-project overrides:

```makefile
GOCYCLO_OVER := 30                                  # default
GOCYCLO_TARGETS := ./cmd/app/main.go                # optional target list
GOCYCLO_BASELINE := .gocyclo-baseline.txt           # default
GOCYCLO_DEFAULT_EXCLUDE_PATHS := _test\.go:         # built-in
GOCYCLO_EXCLUDE_PATHS := gen/:,third_party/:        # optional extra grep -E patterns
```

Targets:

| Target | Behaviour |
| ------ | --------- |
| `lint-gocyclo` | Runs `gocyclo`, diffs normalized findings against `.gocyclo-baseline.txt`, and fails on new findings. |
| `lint-gocyclo-baseline` | Syncs the baseline to the current finding set. |
| `lint-gocyclo-baseline-prune-fixed` | Removes fixed findings from the baseline without accepting new findings. |
| `lint-gocyclo-baseline-accept-new` | Accepts new findings into the baseline while keeping fixed findings saved. |

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
| `-time_now_outside_clock` | Wall-clock reads like `time.Now()`, `time.Since()`, and `time.Until()` outside tests, main packages, or canonical `internal/clock` packages |
| `-goroutine_without_recover` | Goroutines launched without a recovery wrapper |
| `-silent_defer_close` | Deferred `Close()` calls that discard errors silently |
| `-slog_missing_trace_id` | Structured logs missing trace identifiers |
| `-grpc_handler_missing_peer_enrichment` | gRPC handlers that do not enrich logs with peer details |
| `-sensitive_field_in_log` | Structured logs that include fields likely to carry secrets |
| `-nolint_ban` | Any `//nolint` comment in production code |

Default behaviour: pulled via `go install github.com/agoodkind/go-makefile/staticcheck/cmd/staticcheck-extra@latest`, with all bundled analyzers enabled by default. Zero project Makefile setup is required. For time-sensitive library code, keep the real wall-clock source in one repo-local `internal/clock` package and pass that clock into packages that need deterministic tests.

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
STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS := _test\.go:         # built-in
STATICCHECK_EXTRA_EXCLUDE_PATHS := \.pb\.go:,/api/            # optional extra grep -E patterns
STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN := time_now_outside_clock # optional scoped baseline regex
```

Targets:

| Target | Behaviour |
| ------ | --------- |
| `staticcheck-extra` | Runs the custom analyzer set, diffs vs baseline, and fails on new findings. Resolved findings print without failing. |
| `staticcheck-extra-bin` | Internal. Resolves or builds the analyzer binary. |
| `staticcheck-extra-baseline` | Syncs the baseline to the current finding set. |
| `staticcheck-extra-baseline-prune-fixed` | Removes fixed findings from the baseline without accepting new findings. |
| `staticcheck-extra-baseline-accept-new` | Accepts new findings into the baseline while keeping fixed findings saved. |

`make lint` and `make check` both include `staticcheck-extra` automatically.

The baseline starts with a generated-at header, and each baseline entry keeps the analyzer output before a tab-separated metadata suffix, for example:

```text
# staticcheck-extra: generated_at=2026-05-03T18:30:00Z
path/to/file.go:10:2: message<TAB># staticcheck-extra:first_added=2026-05-03T18:30:00Z last_seen=2026-05-03T18:30:00Z
```

The `staticcheck-extra` gate compares only the analyzer output portion, so metadata timestamp changes do not create a new finding.

Focused analyzer runs can set `STATICCHECK_EXTRA_FLAGS`, for example `STATICCHECK_EXTRA_FLAGS=-time_now_outside_clock`. When the selected analyzer has a known baseline scope, fixed-count reporting and baseline mutation targets compare only saved findings in that scope, so unrelated saved findings are not reported as fixed. If a focused run uses an analyzer with no known scope, fixed-count reporting is suppressed, and destructive baseline modes refuse to run unless `STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN` is set to a grep-compatible regex for the intended findings.

Document each baseline entry in a `STATICCHECK-NOTES.md` so the next person does not try to “fix” an intentional exception.

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
