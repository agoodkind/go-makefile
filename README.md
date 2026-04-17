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
