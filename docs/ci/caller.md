# CI caller

A consumer runs the reusable CI workflow through one job in its own `ci.yml`. That job grants `contents: read`, `id-token: write`, and `attestations: write`, and sets `secrets: inherit`.

## Required permissions

The caller job must grant `attestations: write`. Without it the run fails at startup, because GitHub caps a called workflow's token at the caller's grant, and the reusable workflow's release-package job requests `attestations: write` for its attestation step. The caller also grants `contents: read` for checkout and `id-token: write` for the release build's OIDC proof.

## Signing secrets

The caller sets `secrets: inherit` so the reusable workflow's release dry run receives the consumer's signing material and signs the same way a real release does. A caller without it still passes CI, but its release dry run skips signing and stops exercising that path.

## Cgo setting

The caller passes the same `cgo` value as the consumer's release caller, because the compile matrix builds with that setting. A binary that fails the release's cgo-stub check with cgo off then fails CI before merge instead of failing the release after it. Both reusable workflows default `cgo` to false, so a consumer that sets it in neither place already matches, and a consumer whose release builds with cgo passes `cgo: true` to CI. `go-mk scaffold` writes the release caller's value into the CI caller job that shares its `working_directory` whenever the two differ.

## Scaffold owns the caller

`go-mk scaffold` scaffolds this caller when none exists and repairs an existing caller that calls the reusable CI workflow, so a drifted caller is fixed by re-running scaffold rather than by hand. The [canonical caller](../../cmd/go-mk/scaffold_assets/ci.yml) is the shape scaffold scaffolds and repairs toward.

## Test environment

The reusable workflow sets `GH_TOKEN` to the job token for every gate, including the Test gate. A consumer test that reads `GH_TOKEN` or `GITHUB_TOKEN` as a config fallback sees that token in CI even when the test expects none, so it clears those variables (for example with `t.Setenv("GH_TOKEN", "")`) to stay hermetic. The [reusable CI workflow](../../.github/workflows/_ci.yml) sets the variable at the workflow level.

## Run quality gates in a custom job

After checkout and Go setup, call the [quality gate action](../../.github/actions/quality-gate/action.yml) once for each Make target the job needs. Set `working-directory` for modules outside the repository root. Use the same go-makefile revision for the action and the Makefile bootstrap.

```yaml
- name: Vet the provider
  uses: agoodkind/go-makefile/.github/actions/quality-gate@main
  with:
    gate: vet
    working-directory: provider

- name: Run golangci-lint
  uses: agoodkind/go-makefile/.github/actions/quality-gate@main
  with:
    gate: lint-golangci
    working-directory: provider
```

The action accepts `vet`, `test`, `lint-golangci`, `lint-format`, `lint-gocyclo`, `lint-deadcode`, `staticcheck-extra`, `govulncheck`, and `go-version-check`. Use `build-check` to run the configured group of build checks in one step, including the cgo and platform stub checks. Each step returns the result of its Make target.

The action restores golangci-lint analysis for `lint-golangci` and for `build-check` when `LINT_GATES` includes that linter. The quality gate manifest reports cache paths and keys. The action uses `go-mk-golangci-cache-save-decision` to verify the lint cache before saving it. All gates can reuse the Go build and module caches configured by the job.

For jobs that compile cgo packages, pass `cgo-toolchain-id` from the compile job that saved the native dependency cache. Set `GO_MK_TARGET_GOOS` and `GO_MK_TARGET_GOARCH` in the environment that runs Make. The gate restores the dependency cache when the manifest reports a matching key.

The action runs Make in the job environment, including inside a GitHub Actions job container. Jobs that start a Docker Compose service can call the same Make targets inside that service. To use the gate cache, set `GO_MK_CI_QUALITY_GATE` to the target and run `make go-mk-quality-gate-manifest`. Make the service's cache paths available at the same absolute paths to the steps that restore and save them. Mount the current step's `GITHUB_OUTPUT` file and set `GITHUB_OUTPUT` inside the service to its mount path.
