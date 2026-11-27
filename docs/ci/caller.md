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

## Cache lint analysis in a custom job

Run `make go-mk-cache-manifest` in the same module and environment as the lint check. The manifest writes `lint_cache_paths` to `GITHUB_OUTPUT`. If the check runs in a container, mount the checkout at the same absolute path on the runner and in the container. Mount the step's `GITHUB_OUTPUT` file and point the container's `GITHUB_OUTPUT` variable at that mount.

Pass the actual linter version and a cache scope unique to the module and job. The restore action uses the runner's OS and architecture, the scope, the linter version, and the dependency hash to select a cache. It creates a new primary key each ISO week in UTC and can restore analysis from an earlier key in the same scope. Run the check on every restore result. After a successful check, save the analysis with the primary key if the restore did not find that exact key. [Restore action](../../.github/actions/restore-golangci-cache/action.yml)

This example assumes the workflow has checked out a root Go module and set up Go.

```yaml
- name: Read the Go cache manifest
  id: manifest
  run: make go-mk-cache-manifest

- name: Restore golangci-lint analysis
  id: lint-cache
  uses: agoodkind/go-makefile/.github/actions/restore-golangci-cache@main
  with:
    cache-paths: ${{ steps.manifest.outputs.lint_cache_paths }}
    cache-scope: root-quality
    golangci-lint-version: v2.14.0
    dependency-hash: ${{ hashFiles('go.mod', 'go.sum', 'go.work', 'go.work.sum') }}

- name: Run the quality checks
  run: make build-check

- name: Save golangci-lint analysis
  if: steps.lint-cache.outputs.cache-hit != 'true'
  uses: actions/cache/save@v6
  with:
    path: ${{ steps.manifest.outputs.lint_cache_paths }}
    key: ${{ steps.lint-cache.outputs.cache-primary-key }}
```
