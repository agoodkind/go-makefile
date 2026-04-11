# go-makefile

Shared Go build targets and reusable GitHub Actions workflows for all `agoodkind` Go projects.

## What's in here

| File | Purpose |
|------|---------|
| `go.mk` | Shared Makefile targets: `lint`, `fmt`, `vet`, `test`, `govulncheck`, `check`, `sync` |
| `golangci-template.yml` | Canonical golangci-lint config — projects extend this |
| `goreleaser-template.yaml` | Canonical goreleaser config — bootstrap fills in binary name |
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
- `Makefile` — with runtime-fetch `go.mk` bootstrap + project-specific targets
- `.golangci.yml` — extends the shared lint config
- `.goreleaser.yaml` — filled in with the inferred binary name
- `.gitignore` entry for `.make/`

Skips any file that already exists. Fails clearly if `go.mod` is missing.

---

## How it works

### `go.mk`

Fetched at runtime into `.make/go.mk` — never committed. Any `make` target on a fresh clone auto-bootstraps via curl with a `~/.cache/go-makefile/go.mk` fallback. Run `make sync` to force-update.

Provides: `lint`, `fmt` (gofumpt + goimports), `vet`, `test`, `govulncheck`, `check`, `sync`.

### `.golangci.yml`

Committed per-project. The bootstrap generates a minimal file that `extends` the canonical config:

```yaml
extends:
  - https://raw.githubusercontent.com/agoodkind/go-makefile/main/golangci-template.yml
```

Add project-specific overrides below the `extends` line.

### `.goreleaser.yaml`

Committed per-project. The bootstrap fetches `goreleaser-template.yaml` and substitutes the binary name. Edit as needed after bootstrap.

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

Four independent required checks: `Build and Test`, `Vet`, `Govulncheck`, `GoReleaser Config Check`.

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
