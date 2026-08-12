# Stable cache keys for split restore and save

Split cache saves reuse the primary key captured by their restore step. A job may
create ignored dependency files after restore without changing the key used to
save that cache.

## Problem

Fresh checkouts lack the generated `go.work` and `go.work.sum` files. Cache
restore therefore hashes only the dependency files present at checkout. Build
prerequisites later create the workspace files, and a save step that repeats the
`hashFiles(...)` expression produces a different key.

The next runner requests the checkout-time key again. It can restore the saved
entry only through a broad fallback, so `cache-hit` stays `false` and another
unreachable primary entry is saved.

## Decision

Every explicit `actions/cache/restore` and `actions/cache/save` pair uses one
captured primary key. The restore step keeps its existing key expression. The
save step uses the restore step's `cache-primary-key` output.

The save step must not use `cache-matched-key`. A fallback restore reports the
fallback entry through that output. Saving under it would preserve the old entry
instead of creating the requested primary entry.

Apply this invariant to every paired cache in the [reusable continuous
integration workflow](../../../.github/workflows/_ci.yml), [build
workflow](../../../.github/workflows/_build.yml), and [package
workflow](../../../.github/workflows/_package.yml). This includes pairs whose
current key inputs cannot change during a job. One rule prevents later edits
from reintroducing the defect.

The Go setup action remains unchanged. It owns its restore and post-job save
state within one action rather than exposing a split pair in these workflows.

## Data flow

1. The restore step evaluates its primary key once.
2. The restore action publishes that exact value as `cache-primary-key`.
3. An exact hit sets `cache-hit` to `true` and skips the existing save path.
4. A fallback hit or miss leaves `cache-hit` unequal to `true`.
5. The existing save decision runs after the cache producer completes.
6. The save step writes under the captured primary key, regardless of later
   workspace changes.

Existing cache paths, restore prefixes, generation rules, and save eligibility
remain unchanged.

## Failure behavior

A save step keeps the same eligibility conditions as its restore step, plus its
existing miss or validation condition. A skipped restore therefore cannot reach
its save. A restore failure retains the action's existing behavior. The design
adds no recovery path that reconstructs or recalculates a missing key.

## Verification

Consumer acceptance runs `lm-semantic-search` continuous integration twice from
the same dependency state. The first run may restore a fallback and then save
the requested primary key. The second run must report an exact hit for the
Golangci and compiler caches and must skip their save steps. This live test
exercises cache restore and save input and output through GitHub Actions. A
source-text assertion cannot prove that behavior.

## Rejected approaches

### Precompute every key

A dedicated step could calculate each key before restore and expose it to both
actions. This works but duplicates key plumbing that the restore action already
provides.

### Generate the workspace before restore

Early workspace generation would make ignored files available to `hashFiles`.
It would also change build ordering and duplicate generation work. It does not
protect other keys from future job-time mutations.

### Use one combined cache action

The combined action captures one key automatically. It also owns the post-job
save decision, which would bypass the current cache-specific validation and
success conditions.

## Acceptance criteria

- Every split save uses its paired restore's `cache-primary-key` output.
- No split save repeats its restore key expression.
- Existing cache paths, restore prefixes, and save decisions remain intact.
- Two unchanged consumer runs produce exact Golangci and compiler cache hits on
  the second run.
