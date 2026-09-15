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
