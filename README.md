# go-makefile

Shared Go build, lint, and release automation for `agoodkind` Go projects, driven
by one fetched file, `go.mk`.

## Adopt it in your repo

1. Run from the repo root:
   `curl -fsSL https://raw.githubusercontent.com/agoodkind/go-makefile/main/bootstrap.sh | bash -s -- --yes`
2. Commit the generated or repaired `Makefile`, `bootstrap.mk`, `.github/workflows/ci.yml`, and `.gitignore`.
3. CI: bootstrap scaffolds `.github/workflows/ci.yml` when none exists, calling `uses: agoodkind/go-makefile/.github/workflows/_ci.yml@main`. The reusable workflow grants `contents: read` and `id-token: write` to the build job so `make build` can verify GitHub Actions OIDC proof before skipping its inline build gate. The scaffolded workflow triggers on `push` to every branch (`branches: ['**']`) with a `concurrency` cancel group, so every branch runs CI exactly once with no duplicate `pull_request` run; same-repo PRs still show these checks because GitHub matches checks to the head commit SHA, and `'**'` excludes tags so releases are untouched. Bootstrap leaves an existing `ci.yml` unchanged, so a repo that customizes jobs (submodules, apt packages, extra jobs) keeps its workflow while still using this trigger block as the reference. A repo that accepts fork PRs can add a fork-guarded `pull_request` trigger.
4. Releases: add a workflow that sets `uses: agoodkind/go-makefile/.github/workflows/_release.yml@main` with `permissions: contents: write, id-token: write, attestations: write` and `secrets: inherit`. A consumer that ships darwin release artifacts and wants the whole release to fail closed when Apple signing material is absent can also pass `with: require_darwin_codesign: true`. Linux-only releases are unaffected because the check only applies when `RELEASE_PLATFORMS` includes `darwin`.
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
  build, vet, test, govulncheck, and lint target the CI matrix calls, including
  the split legs `lint-golangci`, `lint-deadcode`, and `staticcheck-extra`, so a
  consumer never threads the prerequisite per leg. The textual legs `lint-format`
  and `lint-gocyclo` are excluded because they never compile a package. If that
  repo wants cheap docs-only skips in reusable CI, set `GO_MK_GENERATE_INPUTS`
  to the repo-relative input dirs whose changes should force the gates (for
  example `shim injector api`). If `GO_MK_GENERATE` is set but
  `GO_MK_GENERATE_INPUTS` is empty, `ci-changed` fails safe to `changed=true`
  and always runs. Multiple targets or dirs are space-separated; unset is a
  no-op.
- Do not commit `go.work`; the bootstrap gitignores `go.work` and `go.work.sum`.
  When a repo depends on a module the proxy cannot build on its own (for example
  `gksyntax`), set `GO_MK_WORKSPACE_USE` to the workspace use-paths (for example
  `. third_party/gksyntax`) before `include bootstrap.mk`, and go.mk generates a
  gitignored `go.work` from those paths before each build. See
  [docs/workspace/routing.md](docs/workspace/routing.md) for the policy and the
  reasons behind it.
- Repos that link external C libraries through cgo set `GO_MK_CGO_DEPS` to the
  dependency names before `include bootstrap.mk` and define one
  `go-mk-cgo-dep-<dep>` target per name. go.mk provisions the libraries before
  every compile-bearing target, exports `PKG_CONFIG_PATH`, and resolves the
  cross toolchain into `CC`/`CXX` per build target. See
  [docs/cgo/overview.md](docs/cgo/overview.md) for the contract.
- Repos that ship a self-updating binary or daemon use the `selfupdate` library
  to discover, verify, and install their own releases. `go-mk` uses the same
  package for `go-mk selfupdate`, `go-mk selfupdate watch`, and
  `go-mk selfupdate install-service`. A build with uncommitted changes (marked
  `CurrentDirty`) is guarded from auto-update, and each consumer sources its
  build hash from a runtime hash because the stamped `BinHash` is empty. See
  [docs/selfupdate/overview.md](docs/selfupdate/overview.md) for the contract.
- First installs use the hosted [install.sh](install.sh), which fetches
  `go-mk-install` from the latest `agoodkind/go-makefile` release and delegates
  the consumer binary install to the `selfupdate` package. See
  [docs/installer/overview.md](docs/installer/overview.md) for the flow and
  trust chain.
- Do not pin the lint tools (golangci-lint, gocyclo, deadcode, govulncheck,
  gofumpt, goimports, staticcheck-extra) with a go.mod `tool` directive; go.mk
  installs them itself with versions it controls via the `*_INSTALL` variables,
  and a duplicate directive only drags each tool's transitive graph into the
  module. Bootstrap removes these managed tool directives on every run and leaves
  project-specific tools alone; run `go mod tidy` afterward to prune their
  dependencies.
- Lint gates diff tool findings against committed baseline files and fail only on
  new findings. Bootstrap adds repo-local `.gitignore` allowlist rules for future
  baseline files and `.go-mk-applied-notices`, but it does not create them during
  initial adoption. Commit `.go-mk-applied-notices` only after a notice run creates
  it. Commit baseline files only after a baseline target creates them. Changing a
  baseline requires the token gate.
- Local `make build` runs `build-check` before compiling. The CI split is
  CI-only: the reusable workflow reports each quality gate separately, and the
  build job skips inline gates only after `go-mk` verifies a GitHub Actions OIDC
  JWT for the current repository and run.
- The reusable CI skips the quality and build work on a push that changes nothing
  the Go build or tests depend on. A `changes` job runs `go-mk ci-changed`, which
  decides from `go list -e -deps -json ./...` (so `go:embed` payloads and cgo
  C sources count by construction, and a missing generated embed target does not
  force codegen first) plus the build-config, submodule, `GO_MK_WORKSPACE_USE`,
  and declared `GO_MK_GENERATE_INPUTS` paths. On pushes to the default branch it
  diffs `github.event.before..HEAD`; on feature-branch pushes it diffs from
  `merge-base(origin/<default>, HEAD)` so every push re-evaluates the full branch
  delta and cannot wrong-skip after earlier runs were cancelled. The quality
  matrix and build jobs still run and report their named checks, so required
  status checks stay green; only their steps are skipped. Detection fails safe to
  running every gate on any uncertainty (new branch on trunk, force push,
  `go list` error, merge-base failure, non-`push` event, or a codegen repo that
  sets `GO_MK_GENERATE` without `GO_MK_GENERATE_INPUTS`). Set
  `skip_unchanged: false` to always run the gates. A consumer's own Go job can
  ride the same signal with `needs: <reusable job>` and
  `if: needs.<job>.outputs.changed == 'true'`.
- Specifics live in source: `cmd/go-mk/bootstrap.go` (what bootstrap writes),
  `go.mk` (targets and their knobs), `golangci.yml` (lint config),
  `staticcheck/` (bundled analyzers), `.github/workflows/` (CI and release
  jobs).
