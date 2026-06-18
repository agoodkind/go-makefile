# go-makefile

Shared Go build, lint, and release automation for `agoodkind` Go projects, driven
by one fetched file, `go.mk`.

## Adopt it in your repo

1. Run from a repo root that has a `go.mod`:
   `curl -fsSL https://raw.githubusercontent.com/agoodkind/go-makefile/main/bootstrap.sh | bash`
2. Commit the generated `Makefile`.
3. CI: add a workflow that sets `uses: agoodkind/go-makefile/.github/workflows/_ci.yml@main`. The reusable workflow grants `contents: read` and `id-token: write` to the build job so `make build` can verify GitHub Actions OIDC proof before skipping its inline build gate.
4. Releases: add a workflow that sets `uses: agoodkind/go-makefile/.github/workflows/_release.yml@main` with `permissions: contents: write` and `secrets: inherit`.
5. Run `make help` to list targets. `make check` is the default.

## Notes

- `go.mk` and its helpers fetch into `.make/` on every run and are never
  committed; the bootstrap gitignores `.make/`. Run `make update-go-mk` to
  refetch. Set `GO_MK_DEV_DIR` to a local go-makefile checkout to fetch from
  there instead of `main`.
- Lint gates diff tool findings against committed baseline files and fail only on
  new findings. Changing a baseline requires the token gate.
- Local `make build` runs `build-check` before compiling. The CI split is
  CI-only: the reusable workflow reports each quality gate separately, and the
  build job skips inline gates only after `go-mk` verifies a GitHub Actions OIDC
  JWT for the current repository and run.
- Specifics live in source: `bootstrap.sh` (what bootstrap writes), `go.mk`
  (targets and their knobs), `golangci.yml` (lint config), `staticcheck/`
  (bundled analyzers), `.github/workflows/` (CI and release jobs).
