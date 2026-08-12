# Stable Cache Save Keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every split cache save use the primary key captured before its paired restore runs.

**Architecture:** Each existing restore step remains the only place that evaluates its primary key. Its paired save step consumes the restore action's `cache-primary-key` output, so files created during the job cannot change the save key. Live consumer runs verify the GitHub Actions input and output behavior.

**Tech Stack:** GitHub Actions, `actions/cache/restore@v6`, `actions/cache/save@v6`, Go workflow tests, and `actionlint`.

## Global Constraints

- Keep every restore key, cache path, fallback prefix, condition, and save decision unchanged.
- Never use `cache-matched-key`.
- Cover generated outputs, cgo dependencies, ccache, Darwin ccache, golangci-lint, and quill.
- Verify cache behavior through GitHub Actions input and output. Do not use a source-text assertion as behavioral proof.
- Follow the [stable cache key design](../specs/2026-08-12-cache-primary-key-design.md).

---

## File Structure

**Modified:**

- The reusable continuous integration workflow contains the prepare, compile, and quality cache pairs.
- The reusable build workflow contains generated output, cgo dependency, Linux ccache, and Darwin ccache pairs.
- The reusable package workflow contains the quill cache pair.
- The release workflow tests retain meaningful Darwin ccache behavior assertions after save-key reconstruction is removed.

**Created:**

- This plan defines the implementation and acceptance sequence.

---

### Task 1: Reuse restore primary keys in every split save

**Files:**

- Modify: [Reusable continuous integration workflow](../../../.github/workflows/_ci.yml)
- Modify: [Reusable build workflow](../../../.github/workflows/_build.yml)
- Modify: [Reusable package workflow](../../../.github/workflows/_package.yml)

**Interfaces:**

- Consumes: The `cache-primary-key` output published by each `actions/cache/restore@v6` step.
- Produces: Nine `actions/cache/save@v6` steps that use the exact key requested before restore.

- [ ] **Step 1: Record the paired restore IDs**

Use this mapping without changing the restore side:

| Workflow | Save step | Restore step ID | Count |
| --- | --- | --- | ---: |
| Continuous integration | `Save generated outputs` | `generated-cache-restore` | 2 |
| Continuous integration | `Save ccache` | `ccache-restore` | 1 |
| Continuous integration | `Save golangci-lint cache` | `golangci-cache-restore` | 1 |
| Build | `Save generated outputs` | `generated-cache-restore` | 1 |
| Build | `Save cgo deps` | `cgo-cache-restore` | 1 |
| Build | `Save ccache` | `ccache-restore` | 1 |
| Build | `Save darwin ccache` | `darwin-ccache-restore` | 1 |
| Package | `Save quill` | `quill-cache` | 1 |

- [ ] **Step 2: Replace each save key**

Set each save key to its paired restore output:

```yaml
key: ${{ steps.generated-cache-restore.outputs.cache-primary-key }}
key: ${{ steps.cgo-cache-restore.outputs.cache-primary-key }}
key: ${{ steps.ccache-restore.outputs.cache-primary-key }}
key: ${{ steps.darwin-ccache-restore.outputs.cache-primary-key }}
key: ${{ steps.golangci-cache-restore.outputs.cache-primary-key }}
key: ${{ steps.quill-cache.outputs.cache-primary-key }}
```

Do not edit the save step's `path` or `if` expression. Do not edit the restore step's `key` or `restore-keys` values.

- [ ] **Step 3: Validate workflow syntax**

Run:

```bash
actionlint .github/workflows/_ci.yml .github/workflows/_build.yml .github/workflows/_package.yml
```

Expected: exit status 0 with no findings.

---

### Task 2: Keep only meaningful local workflow assertions

**Files:**

- Modify: [Release workflow tests](../../../cmd/go-mk/releaseworkflow_test.go)

**Interfaces:**

- Consumes: Existing helpers that isolate named workflow steps.
- Produces: Darwin ccache tests that still verify restore configuration, cache paths, save eligibility, and workflow ordering without claiming to prove GitHub cache input and output.

- [ ] **Step 1: Remove the stale save-key reconstruction assertion**

Keep these assertions for `Save darwin ccache`:

```go
requireWorkflowContains(t, save, "if: matrix.cc != '' && inputs.cgo && steps.darwin-ccache-restore.outputs.cache-hit != 'true'")
requireWorkflowContains(t, save, "uses: actions/cache/save@v6")
requireWorkflowContains(t, save, "path: ~/.ccache")
```

Remove the assertion that expects `darwinCcacheWorkflowKey()` in the save step. The restore test remains the local owner of the reconstructed restore key.

- [ ] **Step 2: Run the focused Go test**

Run:

```bash
go test ./cmd/go-mk -run TestBuildWorkflowConfiguresDarwinCcache -v
```

Expected: PASS.

- [ ] **Step 3: Run the full Go suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run repository checks**

Run:

```bash
make check
git diff --check
```

Expected: both commands exit with status 0.

---

### Task 3: Review, sign, and merge the framework change

**Files:**

- Modify only files listed in Tasks 1 and 2.

**Interfaces:**

- Consumes: Green local verification and the complete branch diff.
- Produces: A signed pull request commit on `go-makefile` `main`.

- [ ] **Step 1: Run adversarial review**

Verify all nine save and restore pairings, unchanged paths and conditions, clean merge against current `origin/main`, and the absence of `cache-matched-key`.

- [ ] **Step 2: Create signed commits**

Run `git commit -S` with the required Codex co-author trailer. Verify the framework commits with these commands and inspect each raw commit for a `gpgsig` header:

```bash
git verify-commit 38991383a350986527f331dfb6afcc3bf868262c
git verify-commit a9777b1cbc684628b84358d13f489b25536c3f10
git cat-file commit 38991383a350986527f331dfb6afcc3bf868262c
git cat-file commit a9777b1cbc684628b84358d13f489b25536c3f10
```

- [ ] **Step 3: Publish and merge**

Create one pull request against `main`. Merge only after required checks pass and all review threads are resolved.

- [ ] **Step 4: Reconcile the trunk checkout**

Fetch `origin`, fast-forward the checkout that owns `main`, and remove the contained feature branch and worktree.

---

### Task 4: Prove cache input and output in lm-semantic-search

**Files:**

- No persistent consumer changes.

**Interfaces:**

- Consumes: The reusable workflow from `go-makefile` `main`.
- Produces: Two successful GitHub Actions attempts at one `lm-semantic-search` commit, with exact second-pass cache hits and skipped saves.

- [ ] **Step 1: Trigger a real consumer run**

Use one signed temporary Go change so change detection runs every gate. Push that commit to a temporary branch and wait for the full `lm-semantic-search` continuous integration workflow to pass.

- [ ] **Step 2: Rerun the exact event**

Run:

```bash
gh run rerun 31603276324 -R agoodkind/lm-semantic-search
```

Confirm attempt 2 reports the same `headSha` as attempt 1.

- [ ] **Step 3: Verify exact cache hits**

Inspect attempt 2 logs. Require `Cache restored from key:` to report the requested primary keys for:

- `go-mk-golangci-...`
- Linux `ccache-...`
- Darwin `ccache-...`

Use the attempt 2 jobs API to require these step conclusions:

- `Restore golangci-lint cache`: `success`
- `Save golangci-lint cache`: `skipped`
- Each applicable compiler cache restore: `success`
- Every paired compiler cache save: `skipped`

- [ ] **Step 4: Remove acceptance artifacts**

Delete the temporary remote branch, local branch, worktree, and synthetic commit. Leave the consumer's existing checkout unchanged.
