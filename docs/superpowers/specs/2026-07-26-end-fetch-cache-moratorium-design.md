# go-mk: end the fetch cache moratorium with a delegating bootstrap

Date: 2026-07-26
Status: awaiting review
Target: `agoodkind/go-makefile`
Peer: swift-makefile takes the same treatment. See its own spec.

## Problem

Three defects share one cause: fetch policy lives in `bootstrap.mk`, which every
consumer commits a copy of.

**An offline parse destroys the consumer's assets.** `_go_mk_prime`
(`bootstrap.mk:50`) deletes `.make/go.mk`, `.make/golangci.yml`, and every
module before it knows the download will succeed. Reproduced on 2026-07-26 in a
temp consumer with a warm `.make`, pointing the codeload repo and the raw base
at unreachable hosts:

```
error: go.mk fetch failed; no cache fallback (moratorium). ...
error: golangci.yml fetch failed; no cache fallback (moratorium). ...
make: *** No rule to make target `help'.  Stop.
go.mk=DESTROYED
golangci.yml=DESTROYED
```

The consumer cannot run again until the network returns. This is not the
fail-loud behavior the moratorium intended, and no rule reachable from `go.mk`
can prevent it, because the delete happens before `go.mk` is included.

**The same tarball downloads twice per parse.** `bootstrap.mk:52` pulls it for
`go.mk`, `golangci.yml`, and the modules, then `go.mk:37` pulls it again for the
helper scripts and `notices.txt`. About 640 KB per parse for one snapshot.

**There is no reuse when the network fails.** Commit `b454036` (2026-05-04)
removed the on-disk cache and recorded why in the since-deleted
`bootstrap-include.sh`:

> the on-disk cache fallback was removed because a stale cache silently masked
> an upstream go.mk breakage and froze every consumer on a broken pipeline.
> Restore the cache only after the primary fetch path (gh-auth + raw fallback)
> has been demonstrably reliable for a sustained period. Until then, fail loud
> rather than serve stale.

That moratorium text survives in `bootstrap.mk:25-27,36`, the embedded copy at
`cmd/go-mk/bootstrap_assets/bootstrap.mk`, and `scripts/go-mk-fetch-one.sh:57`.
The path it distrusted no longer exists: the `gh api` contents tier is gone, and
a single codeload tarball is primary with the raw CDN as the per-file fallback.

## What makes reuse safe

The old cache was TTL-based. Inside a five-minute window it skipped the network
entirely, so an upstream break was invisible, and once fetches began failing its
fallback served the cached copy indefinitely. Nothing ever asked upstream whether
the cached copy was still correct.

`codeload.github.com` answers exactly that for less than the cost of today's
unconditional download. It returns an `ETag`, and a request carrying
`If-None-Match` returns `304` with an empty body.

Measured on 2026-07-26 over a link with 201ms average round trip time to
`codeload.github.com`:

| request | result | time | bytes |
| --- | --- | --- | --- |
| `tar.gz/main` with `If-None-Match` | `304` | 0.76-1.18s, median 0.88s | 0 |
| `tar.gz/main` cold | `200` | 2.06s | 320,665 |
| `git ls-remote` as a separate probe | sha | 1.63-1.77s | n/a |
| `info/refs?service=git-upload-pack` as a separate probe | sha | 0.97-1.08s | 8,472 |

A separate probe costs about as much and still needs the download afterward, so
one conditional request is both the question and the fetch. The cost is round
trip bound at roughly 4.3 RTT plus DNS, so the same `304` should land near
0.1-0.2s on a 20ms link.

The `ETag` is a content hash, so it validates asset content rather than commit
identity. A commit that leaves the tree unchanged keeps the same `ETag`, and
serving disk in that case is correct.

The rule is therefore: never skip the network, and serve disk only when upstream
itself confirmed nothing moved. That answers the incident structurally rather
than statistically, so it does not depend on first measuring the primary path's
reliability.

## Consumer cost

Fetched files reach consumers on their next run at no cost. `bootstrap.mk` does
not: consumers commit it, `reconcileBootstrapMk` (`cmd/go-mk/bootstrap.go`)
writes the embedded copy into a consumer tree, and each consumer still needs a
reviewed and merged PR. The fleet is over a dozen repositories.

This design therefore spends one `bootstrap.mk` PR round and makes it the last
one needed for fetch behavior, by moving all policy out of the consumer-committed
file.

## Goals

- An offline parse never removes an asset it cannot replace.
- A parse whose upstream has not moved transfers zero asset bytes.
- One tarball download per parse, at most.
- Upstream is consulted on every parse that touches the network.
- Serving disk after a failed validation is bounded, loud, and never in CI.
- After this change, a fetch-policy change requires no consumer PR.
- No new user-facing variable. `GO_MK_SKIP_FETCH` stays the only knob.

## Non-goals

- A shared cross-worktree cache under `~/.cache`. Each worktree's `.make/` is
  its own store. A shared store is how the golangci results cache poisoned a
  sibling worktree (see `2026-06-07-go-mk-cache-poisoning-hardening-design.md`).
- Pinning consumers to a ref. `GO_MK_API_REF` keeps its current meaning.
- Deleting `go.mk`'s current provisioning path in this change. It stays for
  mixed-version parses and is removed in a later cleanup.

## Design

### The delegating bootstrap

`bootstrap.mk` keeps its variables, obtains one helper script, runs it, and
includes `go.mk`. It holds no fetch policy beyond obtaining the helper:

```
GO_MK_BOOTSTRAP := .make/scripts/go-mk-bootstrap.sh
```

Obtaining the helper is the only fetch rule left in consumer-committed code, and
it is deliberately non-destructive: a dev override, then an existing copy, then
one raw fetch, and a loud failure only when the helper is absent and
unreachable. It never deletes an existing helper, so a cold offline start is the
single unavoidable hard failure, and that case cannot work under any design.

The helper URL derives from `GO_MK_API_REPO` and `GO_MK_API_REF` rather than
from `GO_MK_BASE_URL`, whose value ends in `/main` and would pin a ref-pinned
consumer's helper to `main`. `GO_MK_BASE_URL` remains honored as an override.

The helper then provisions every asset and owns every decision below. A policy
change ships in the helper, which is fetched, so no consumer PR follows.

The helper refreshes itself as part of the tree it extracts, so a new helper
applies on the next parse. That one-parse lag matches how `swift.mk` already
refreshes itself and is not new behavior.

### One extraction provisions everything

The helper extracts one tarball and provisions `go.mk`, `golangci.yml`, the
`GO_MK_MODULES`, the helper scripts, and `notices.txt` together. The duplicate
download disappears by construction rather than by coordination, since there is
only one component doing the fetching.

### Non-destructive provisioning

The helper extracts into a staging directory, verifies every required asset is
present and non-empty there, and only then replaces the files under `.make/`.
Nothing is deleted before its replacement exists. A failed or partial download
leaves the previous assets exactly as they were, which is what makes the
reuse rows below reachable at all.

### Fetch state

`.make/.go-mk-fetch-state` records three fields, written only after a successful
validation or a completed provision: the resolved ref, the codeload `ETag`, and
the Unix timestamp of that success. It lives beside the assets it describes, so a
fresh worktree starts cold and no state outlives what it refers to.

### The decision table

"Assets" means every required file, present and non-empty.

| state | assets | conditional GET | action |
| --- | --- | --- | --- |
| `GO_MK_SKIP_FETCH=1` | present | not attempted | serve disk, unchanged from today |
| missing | any | unconditional | full provision, fail loud on failure |
| present | incomplete | unconditional | full provision, fail loud on failure |
| present | present | `304` | serve disk, refresh the timestamp, transfer nothing |
| present | present | `200` | stage, verify, replace, record the new `ETag` |
| present | present | timeout or error, state at most 1 hour old | serve disk, print one warning |
| present | present | timeout or error, state over 1 hour old | force the full provision, fail loud on failure |

The validation request carries `--connect-timeout 2 --max-time 3`. The measured
`304` uses 0.88s of that on a 201ms link and roughly 0.2s on a fast one, so
ordinary latency never reaches the cap and only genuine breakage lands in the
timeout rows.

One hour bounds how far a serve can drift from upstream. A developer whose
network drops mid-session keeps working; a checkout that has not validated since
before a break forces a real fetch and fails loud rather than running on old
engine assets.

The warning names what is served and how old it is, on one line:

```
go-makefile: upstream unreachable; serving .make assets validated 12m ago (etag 4aeaf3db). Set GO_MK_SKIP_FETCH=1 to silence, or check network access to codeload.github.com
```

### CI never serves disk

A real GitHub Actions run is `GITHUB_ACTIONS=true` with a non-empty
`GITHUB_RUN_ID`, the test `runBuildGateWith` and swift-makefile's
`Build.runsInlineGates` already use. `GITHUB_ACTIONS` alone is not a CI run.

Under that condition the helper neither reads nor writes state, makes no
conditional request, and provisions unconditionally with fail-loud. A green CI
run can never rest on a stale engine, and CI adds no latency and no new failure
mode.

### go.mk during and after migration

A consumer with an old `bootstrap.mk` and a new `go.mk` must still parse, so
`go.mk` keeps its current provisioning path. It skips that path when the helper
already provisioned this run, which it detects from the state the helper wrote,
and otherwise behaves as it does today.

Once the fleet has migrated, `go.mk`'s `_go_mk_prime`,
`go_mk_fetch_bootstrap`, and `go-mk-fetch-one` machinery become unreachable and
are removed. That cleanup is deliberately not part of this change.

### Dead variables

`go.mk:10,11,15` define `GO_MK_URL`, `GO_MK_CACHE`, and `GO_MK_CACHE_DIR`, and
nothing reads any of them. `GO_MK_CACHE` and `GO_MK_CACHE_DIR` are leftovers of
the removed `~/.cache` tier and would misdescribe the design replacing it. All
three are removed.

### Moratorium text

The `TODO(moratorium)` comments and the "no cache fallback (moratorium)" error
strings are replaced by comments stating the validation rule. The genuine
fail-loud messages keep naming `GO_MK_DEV_DIR` and the reachability check and
drop the moratorium reference.

## Error handling

- A malformed or unreadable state file is treated as missing, so the run takes
  the cold path rather than trusting a value it cannot parse.
- A `304` whose assets are incomplete is treated as a miss and forces the
  unconditional provision, so validation can never bless a partial `.make/`.
- The state is written only after every asset is in place, so an interrupted
  provision cannot leave a recent timestamp over a broken tree.
- A future timestamp, which a backwards clock produces, is treated as stale and
  forces the provision.
- The helper is replaced as part of the staged tree, so a failed provision never
  leaves a truncated helper behind.

## Testing

Each row of the decision table gets a case that runs the real entry point.

A local HTTP server serves a real tarball, returns a genuine `ETag`, and honors
`If-None-Match`. A temp consumer repo with a committed `bootstrap.mk` runs
`make` against it with `GO_MK_API_REPO` and `GO_MK_BASE_URL` pointed at the
server. Each case asserts what an operator would see: the server's request count
and response codes, bytes transferred, which files under `.make/` changed
content, the exit status, and the warning text.

- Cold: no `.make/`, one `200`, assets present, state written.
- Warm unchanged: one `304`, zero bytes, no asset content change.
- Warm moved: the server advances the tarball, one `200`, assets updated, new
  `ETag` recorded.
- Offline non-destruction: warm `.make/`, server unreachable, and every asset
  still present afterward with its original content. This is the regression test
  for the reproduction above and must fail against today's `bootstrap.mk`.
- Timeout recent: the server sleeps past the cap, state stamped 10 minutes ago,
  assets served, exit 0, warning printed naming the age.
- Timeout stale: state stamped 2 hours ago, the forced provision also fails,
  exit non-zero, no warning claiming a serve.
- CI: `GITHUB_ACTIONS=true` and `GITHUB_RUN_ID=1` with fresh state and complete
  assets still issues an unconditional request.
- Skip fetch: `GO_MK_SKIP_FETCH=1` issues no request at all.
- Single download: one `make` parse produces exactly one tarball request.
- Mixed version: an old `bootstrap.mk` with the new `go.mk` still parses and
  provisions correctly.

`make smoke-fetch` (`go.mk:514`) keeps its current meaning as the cold-path
proof and gains no new responsibility.

## Rollout

1. Land the helper and the `go.mk` changes. Both are fetched, so every consumer
   gets the validated reuse for the `go.mk`-owned assets, the removal of the
   second tarball download, and the dead-variable cleanup on its next run, with
   no PR.
2. Land `bootstrap.mk` and the embedded copy at
   `cmd/go-mk/bootstrap_assets/bootstrap.mk` together, since the embedded copy is
   what `reconcileBootstrapMk` writes and what a new consumer scaffolds from.
3. Run the consumer update and merge the resulting PRs. This is the round that
   ends the destructive offline parse and the duplicate download.
4. Later, once every consumer has migrated, remove `go.mk`'s superseded
   provisioning machinery.

## Files touched

- `scripts/go-mk-bootstrap.sh` (new; the helper, owning the decision table,
  staged provisioning, state, and the CI rule)
- `bootstrap.mk` (delegate to the helper; keep only variables, helper
  acquisition, and the include)
- `cmd/go-mk/bootstrap_assets/bootstrap.mk` (embedded copy, identical change)
- `go.mk` (skip provisioning when the helper already ran; remove `GO_MK_URL`,
  `GO_MK_CACHE`, `GO_MK_CACHE_DIR`)
- `scripts/go-mk-fetch-one.sh` (moratorium text in the failure message)
- `docs/fetch.md` (new; the fetch contract and the reuse rule, as current-state
  behavior)
- tests for the decision table
