# go-makefile

Shared Go build, lint, and release automation for `agoodkind` Go projects, driven
by one fetched file, `go.mk`.

## Adopt it in your repo

1. Run from the repo root:
   `curl -fsSL https://raw.githubusercontent.com/agoodkind/go-makefile/main/bootstrap.sh | bash -s -- --yes`
2. Commit the generated or repaired `Makefile`, `bootstrap.mk`, `.gitignore`, baseline files, and `.go-mk-applied-notices`.
3. CI: add a workflow that sets `uses: agoodkind/go-makefile/.github/workflows/_ci.yml@main`. The reusable workflow grants `contents: read` and `id-token: write` to the build job so `make build` can verify GitHub Actions OIDC proof before skipping its inline build gate.
4. Releases: add a workflow that sets `uses: agoodkind/go-makefile/.github/workflows/_release.yml@main` with `permissions: contents: write` and `secrets: inherit`.
5. Run `make help` to list targets. `make check` is the default.

## Notes

- `bootstrap.sh` is a thin wrapper around
  `go run goodkind.io/go-makefile/cmd/go-mk@main bootstrap`. Pass
  `--module=<path>` for a new repo when inference from git remote or directory
  name is not enough.
- `go.mk` and its helpers fetch into `.make/` on every run and are never
  committed; the bootstrap gitignores `.make/`. Run `make update-go-mk` to
  refetch. Set `GO_MK_DEV_DIR` to a local go-makefile checkout to fetch from
  there instead of `main`.
- Repos that generate source before compiling (for example a tree-sitter parser
  or proto) set `GO_MK_GENERATE` to the codegen target name(s) before
  `include bootstrap.mk`. go.mk runs them as an order-only prerequisite of every
  build, lint, vet, test, and govulncheck target. Multiple targets are
  space-separated; unset is a no-op.
- Do not commit `go.work`; the bootstrap gitignores `go.work` and `go.work.sum`.
  When a repo vendors a module the proxy cannot build on its own (for example
  `gksyntax`, whose generated parser C and nested grammar submodules are not in
  the module zip), set `GO_MK_WORKSPACE_USE` to the workspace use-paths (for
  example `. third_party/gksyntax`) before `include bootstrap.mk`. go.mk
  materializes a gitignored `go.work` from those paths before every build, lint,
  vet, test, and govulncheck target, so fresh checkouts and CI route the module
  without a committed `go.work`. A committed go.mod `replace` is not an option
  here because gomoddirectives rejects local replacements. An existing `go.work`
  is left untouched, so a developer override survives.
- Lint gates diff tool findings against committed baseline files and fail only on
  new findings. Bootstrap touches the baseline files and `.go-mk-applied-notices`,
  and adds repo-local `.gitignore` allowlist rules so they stay tracked.
  Changing a baseline requires the token gate.
- Local `make build` runs `build-check` before compiling. The CI split is
  CI-only: the reusable workflow reports each quality gate separately, and the
  build job skips inline gates only after `go-mk` verifies a GitHub Actions OIDC
  JWT for the current repository and run.
- Specifics live in source: `cmd/go-mk/bootstrap.go` (what bootstrap writes),
  `go.mk` (targets and their knobs), `golangci.yml` (lint config),
  `staticcheck/` (bundled analyzers), `.github/workflows/` (CI and release
  jobs).
