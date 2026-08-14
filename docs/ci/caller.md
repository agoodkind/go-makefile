# CI caller

A consumer runs the reusable CI workflow through one job in its own `ci.yml`. That job grants `contents: read`, `id-token: write`, and `attestations: write`, and sets `secrets: inherit`.

## Required permissions

The caller job must grant `attestations: write`. Without it the run fails at startup, because GitHub caps a called workflow's token at the caller's grant, and the reusable workflow's release-package job requests `attestations: write` for its attestation step. The caller also grants `contents: read` for checkout and `id-token: write` for the release build's OIDC proof.

## Signing secrets

The caller sets `secrets: inherit` so the reusable workflow's release dry run receives the consumer's signing material and signs the same way a real release does. A caller without it still passes CI, but its release dry run skips signing and stops exercising that path.

## Bootstrap owns the caller

`go-mk bootstrap` scaffolds this caller when none exists and repairs an existing caller that calls the reusable CI workflow, so a drifted caller is fixed by re-running bootstrap rather than by hand. The [canonical caller](../../cmd/go-mk/bootstrap_assets/ci.yml) is the shape bootstrap scaffolds and repairs toward.

## Test environment

The reusable workflow sets `GH_TOKEN` to the job token for every gate, including the Test gate. A consumer test that reads `GH_TOKEN` or `GITHUB_TOKEN` as a config fallback sees that token in CI even when the test expects none, so it clears those variables (for example with `t.Setenv("GH_TOKEN", "")`) to stay hermetic. The [reusable CI workflow](../../.github/workflows/_ci.yml) sets the variable at the workflow level.
