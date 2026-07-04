# CI

go-makefile ships a reusable CI workflow that every consumer runs against its own repository. The workflow is a three-stage pipeline defined in [.github/workflows/_ci.yml](../../.github/workflows/_ci.yml): `prepare` detects change and warms the generated-output cache, then the compile matrix and the quality gates run in parallel, and the release dry run consumes the compiled binaries. A consumer enters it through its own `ci.yml`, which calls this workflow at `@main`, so a change here reaches every consumer on its next push.

## The prepare gate

The `prepare` job in [.github/workflows/_ci.yml](../../.github/workflows/_ci.yml) runs first and every other job depends on it. It runs `make ci-changed`, whose engine ([cmd/go-mk/cli.go](../../cmd/go-mk/cli.go) `ci-changed`) writes `changed=<bool>` and fails safe to `changed=true` on any uncertainty, and it decides whether the gates do real work through the `skip_unchanged` input. It also detects whether the repository releases by checking for `.github/workflows/release.yml`.

`prepare` is the single saver of the generated-output cache, which keeps the quality matrix from racing on concurrent cache saves. When `make go-mk-cache-manifest` ([cmd/go-mk/cachemanifest.go](../../cmd/go-mk/cachemanifest.go)) reports `generated_cache_enabled=true`, `prepare` restores the cache, runs `make go-mk-generate` on a miss, and saves it. `go-mk-generate` in [go.mk](../../go.mk) runs only the consumer's `GO_MK_GENERATE` codegen target, so it produces the generated outputs without compiling. A repository with no codegen leaves the flag false, so every warming step is a no-op and the job stays cheap.

## The compile matrix and the build gate

The `compile` job calls the shared build component [.github/workflows/_build.yml](../../.github/workflows/_build.yml), which cross-compiles each platform the `release_platforms` input names and uploads the raw per-platform binaries as `build-<goos>-<goarch>` artifacts. darwin targets cross-compile on Ubuntu inside the goreleaser-cross container by default, or build natively on the runner named by `darwin_runner`. The compile stage runs `make release` with `RELEASE_STAGE=compile`, which reaches `compileStage` in [cmd/go-mk/release.go](../../cmd/go-mk/release.go) and builds without signing or archiving.

The `build` job is the required gate. On a releasing repository it rolls up the compile matrix, so its first step fails when any compile leg failed and the single `go / Build` check reflects compilation under one name regardless of how many platforms the consumer builds. On a repository with no release configuration it runs the host `make build` instead. It always runs, so its named check reports even when the compile matrix was skipped.

## The quality gates

The `quality` matrix in [.github/workflows/_ci.yml](../../.github/workflows/_ci.yml) runs nine gates (Vet, Test, Golangci Lint, Format, Gocyclo, Deadcode, Staticcheck Extra, Govulncheck, Go Version). It depends only on `prepare`, so it runs in parallel with the compile matrix and the release dry run and blocks on neither. Each gate restores the generated cache `prepare` warmed and self-generates on a miss, since `GO_MK_GENERATE` is an order-only prerequisite of every compile gate in [go.mk](../../go.mk). A gate failure blocks merge independently of the build and release results.

## The release dry run

The `release-package` job calls the shared package component [.github/workflows/_package.yml](../../.github/workflows/_package.yml), which downloads the compile artifacts, signs and notarizes the darwin binaries with anchore/quill, archives every platform, attests each archive, and uploads it as `release-<goos>-<goarch>`. It runs `make release` with `RELEASE_STAGE=package`, which reaches `packageStage` in [cmd/go-mk/release.go](../../cmd/go-mk/release.go) and never compiles, tags, or publishes. Signing is skipped when no signing material is present, so a fork or a repository without Apple secrets still archives.

The `release` job is the required gate over that matrix. Its check is named `Release (dry run)` on every branch, because CI never publishes on any branch; the real publish lives in [.github/workflows/release.yml](../../.github/workflows/release.yml) on the default branch. A repository with no release configuration keeps this check green because nothing was supposed to package.

## The shared components and the real release

The compile and package components are the single source of the release build. The real release workflow [.github/workflows/_release.yml](../../.github/workflows/_release.yml) drives the same components in sequence (tag, then compile, then package, then publish, then verify), so the CI dry run exercises the exact path a real release takes. `RELEASE_STAGE` selects the stage in `runReleaseStage` in [cmd/go-mk/release.go](../../cmd/go-mk/release.go); an unknown stage is rejected there rather than falling through to the publishing all-in-one path, so a stray stage name fails safe.

## Required checks

The required merge gates for a consumer are `go / Build`, `go / Release (dry run)`, and the nine `go / Quality / *` checks, held by that repository's branch ruleset rather than by this workflow. The gate names stay uniform across every consumer platform set, so a consumer's ruleset never depends on which platforms it builds. `go / Prepare` and the per-platform compile and package legs report but are not required.
