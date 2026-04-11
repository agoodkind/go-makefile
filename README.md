# go-makefile

Shared Go build targets and reusable GitHub Actions workflows for all `agoodkind` Go projects.

## What's in here

| File | Purpose |
|------|---------|
| `go.mk` | Shared Makefile targets: `lint`, `fmt`, `vet`, `test`, `govulncheck`, `check` |
| `.github/workflows/_ci.yml` | Reusable CI workflow (4 independent jobs, each a required check) |
| `.github/workflows/_release.yml` | Reusable release workflow (timestamp tag + goreleaser) |
| `goreleaser-template.yaml` | Starter `.goreleaser.yaml` to copy into a new project |

---

## Quickstart: adopting in a new Go project

### 1. Add the subtree

```sh
git subtree add --prefix=vendor/go.mk https://github.com/agoodkind/go-makefile.git main --squash
```

This drops `go.mk` (and friends) into `vendor/go.mk/` and records the subtree in git history. No submodule init needed.

### 2. Include in your Makefile

Add one line at the top of your `Makefile`, then define only project-specific targets below it:

```makefile
include vendor/go.mk/go.mk

BINARY := your-project
CMD    := ./cmd/$(BINARY)

.DEFAULT_GOAL := build

.PHONY: build deploy clean

build:
	go build $(CMD)

deploy:
	go install $(CMD)

clean:
	rm -f $(BINARY)
```

Shared targets available immediately: `make lint`, `make fmt`, `make vet`, `make test`, `make govulncheck`, `make check`.

### 3. Wire up CI

Create `.github/workflows/ci.yml`:

```yaml
name: CI

on: [push]

jobs:
  ci:
    uses: agoodkind/go-makefile/.github/workflows/_ci.yml@main
```

This gives you four independent required status checks: `Build and Test`, `Vet`, `Govulncheck`, `GoReleaser Config Check`.

### 4. Wire up releases

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

Every push to `main` creates a release tagged `YYYYMMDDHHmm-<hex-build>-<short-sha>` (e.g. `202604101430-f-a1b2c3d`). A newer push cancels any in-progress release run.

### 5. Copy the goreleaser template

```sh
cp vendor/go.mk/goreleaser-template.yaml .goreleaser.yaml
```

Then edit `.goreleaser.yaml` and set `project_name` and `builds.main`/`builds.binary` to match your project.

---

## Keeping the subtree up to date

When `go-makefile` is updated, pull changes into any project with:

```sh
git subtree pull --prefix=vendor/go.mk https://github.com/agoodkind/go-makefile.git main --squash
```
