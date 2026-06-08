# go-mk: harden against cross-worktree golangci-lint cache poisoning

Date: 2026-06-07
Status: approved, ready for implementation planning

## Problem

`go-mk`'s `lint-golangci` gate reported 37 false "new" findings (godoclint and
gofumpt) on a generated file, `api/daemonpb/daemon.pb.go`, in a consumer repo
(agent-gate). Every finding pointed at a deleted sibling worktree path,
`../../phase1-shelldecomp/api/daemonpb/daemon.pb.go`, a directory that no longer
exists on disk. The file is byte-identical to `origin/main` and contains no real
defect; the gate still failed and blocked the branch.

## Root cause (verified)

golangci-lint's own results cache (`GOLANGCI_LINT_CACHE`, default
`~/Library/Caches/golangci-lint`) is content-addressed: its keys derive from
file content, not file path. The same `daemon.pb.go` content had been linted
earlier in a sibling worktree (`phase1-shelldecomp`) that was then deleted. When
the gate ran in `phase1-finalize`, the identical content hit the cache, and
golangci replayed the cached issues carrying the old worktree's absolute path.

go-mk then laundered that foreign path into a repo-local-looking one. golangci
v2 renders finding paths relative to the config-file directory (verified: with
the config in `.make/`, a local finding prints as `../file.go`; with the config
two levels down, it prints as `../../file.go`). Since go-mk passes
`-c .make/golangci.yml`, a genuine local finding carries one leading `../`, and
the deleted sibling carries two. `extractFindings` (`cmd/go-mk/lint.go`) runs
every finding through `findings.NormalizePath`, whose final step strips leading
`../` segments unconditionally:

```go
for strings.HasPrefix(out, "../") {
    out = out[len("../"):]
}
```

So a local `../cmd/main.go` correctly becomes `cmd/main.go`, but the foreign
`../../phase1-shelldecomp/api/daemonpb/daemon.pb.go` becomes
`phase1-shelldecomp/api/daemonpb/daemon.pb.go`, which keeps the deleted
worktree's name as a fake top-level segment and no longer looks out-of-tree. The
laundered path was keyized and diffed against the baseline, matched nothing, and
surfaced as a new finding.

Evidence collected during investigation:

- Literal `make check` reproduced 37 findings on the sibling path.
- Controlled experiment: warm cache produced 113 `phase1-shelldecomp` lines; a
  fresh `GOCACHE` plus fresh `GOLANGCI_LINT_CACHE` produced 0 issues. One
  variable changed.
- Isolation: fresh `GOCACHE` only still produced 113 lines; fresh
  `GOLANGCI_LINT_CACHE` only produced 0. The golangci cache specifically held
  the poison.
- `strings` over the golangci cache `-d` gob payloads recovered the stored data:
  `daemonpb` 628 hits, `shelldecomp` 1883, `daemon.pb.go` 94,
  `should have a godoc` 24224.
- golangci path base: a probe module showed config in `.make/` yields
  `../main.go` and config in `sub/deeper/` yields `../../main.go`, confirming the
  config-dir-relative rendering.
- End to end: `GOLANGCI_LINT_CACHE=<fresh> make build` passed with
  `lint-golangci ok`, confirming the cache is the sole blocker.

## Goals

- The gate judges only the current worktree's code, regardless of cache state.
- A fresh worktree never inherits a sibling worktree's golangci cache.
- The fix is pure Go in process. No new shell pipelines.
- `NormalizePath` stays unchanged so its awk byte-for-byte oracle test stays
  green.

## Non-goals

- Changing golangci-lint's caching behavior.
- Auto-clearing or re-running the gate on detection.
- Touching the consumer repo's committed `.golangci.yml` (a separate latent
  issue; see below).

## Design

Two independent, in-process Go changes in `go-makefile`.

### Component A: out-of-tree finding filter (detection, drop, warn)

The reliable signal is existence under the worktree root, not the shape of the
`../` prefix. A naive "drop if the path starts with `../`" rule is wrong, because
config-dir-relative rendering gives every legitimate local finding a leading
`../`. Instead: after `NormalizePath` maps a finding to its repo-relative form,
a genuine local finding names a file that exists under the worktree root, while a
laundered foreign finding names a path under a fake top segment (the deleted
worktree's name) that does not exist. This test is base-independent: it holds
whether golangci renders relative to the config dir or to the cwd, and whether
the foreign path had one extra `../` or several.

New pure helper in `internal/findings/findings.go`:

```go
// FindingPath returns the file path in front of the first :line:col: run in
// line, and false when the line has no such run (so it has no parseable path).
func FindingPath(line string) (string, bool)
```

It reuses the existing `locationPattern`. The path is the substring before the
first `:line:col:`.

Wiring in `extractFindings` (`cmd/go-mk/lint.go`): keep the current
match/`NormalizePath`/exclude flow, and add one guard. For each candidate line,
extract its path with `FindingPath`. A line with no parseable path is kept (no
change from today). Otherwise, resolve the path: if relative, join it to the
worktree root after normalization; if absolute, use it directly. The finding is
out-of-tree when the resolved file does not exist under the worktree root. An
out-of-tree finding is skipped and counted; everything else is kept exactly as
today. The filesystem check lives in the command layer, which already does file
I/O; the path extraction stays pure and unit-tested. `NormalizePath` is
untouched, so its awk oracle test stays green.

`extractFindings` returns the dropped count alongside the kept findings. When the
count is positive, the gate emits one `slog.Warn` and one user-facing line, for
example:

```
ignored 37 finding(s) with out-of-tree paths (stale lint cache; run golangci-lint cache clean to clear)
```

### Component B: per-worktree golangci cache isolation (prevention)

In `lintEnv()` (`cmd/go-mk/lint.go`), default `GOLANGCI_LINT_CACHE` to
`filepath.Join(makeDir, "golangci-cache")`, i.e. `.make/golangci-cache`, which is
already gitignored and per-worktree. An existing `GOLANGCI_LINT_CACHE` set by the
caller is respected and never overridden. Only golangci reads this variable, so
other tools are unaffected. golangci creates the directory on demand;
`ensureMakeDir` already creates `.make/`.

### Data flow

```
golangci-lint run  (GOLANGCI_LINT_CACHE=.make/golangci-cache)
   -> raw output (.make/golangci-lint.raw.out)
   -> extractFindings:
        for each candidate line:
          FindingPath -> resolve under root
            file missing under root -> drop, count++
            else                    -> NormalizePath, keep
   -> findings (.make/golangci-lint.out) + dropped count
   -> lintgate.Evaluate vs baseline
   -> gate result (+ warn line if dropped > 0)
```

Component B stops a fresh worktree from loading a sibling's cache. Component A is
the backstop for any worktree that still sees a shared or pre-existing cache.
Both are distinct from the existing `nestedWorktreeRoots` and
`expandedPackageTargets` logic, which filters lint targets and does not help
here, since the poison arrives through the cache from a sibling (not nested)
worktree.

### Error handling

- `FindingPath` is pure and total; a line with no `:line:col:` returns false and
  the line is kept.
- The existence check uses `os.Stat`; a stat error other than not-exist is
  treated as present (keep), so a transient FS error never silently drops a real
  finding.
- The cache directory is created by golangci; go-mk only sets the env var.
- An existing `GOLANGCI_LINT_CACHE` is left untouched.

## Why only golangci (scope rationale)

The cross-worktree poisoning is unique to golangci because its cache is
content-addressed, so identical content in a different worktree collides on the
key and replays the stored absolute path. The other gates are not affected:

- `gocyclo`: fed file paths from `findGoFiles()`, a `WalkDir(".")` of the current
  worktree that prunes `vendor`/`gen`/`third_party` and nested worktrees. It
  parses committed source directly, has no content-addressed cache, and never
  sees cgo-generated output. Verified empirically: 0 sibling lines.
- `deadcode`: uses `go list` plus `GOCACHE`. The cgo cache entry is keyed
  including the absolute source path, because cgo bakes that path into a `//line`
  directive, so a different worktree produces a different key, misses the stale
  entry, and recompiles with its own current path. Verified empirically at the
  same commit that poisoned the cache: deadcode referenced the cgo files
  (`limits.go`, `binding.go`) but with current paths, 0 sibling lines, exit 0.
  The 3 stale `GOCACHE` cgo entries from the deleted worktree are dead; nothing
  replays them.

`deadcode` routes its output through `extractFindings`, so Component A covers it
for free as a harmless backstop. It is not a needed fix. Component B is
golangci-specific by necessity.

## Out of scope (related, not addressed here)

The consumer repo committed a `.golangci.yml` containing only `version: "2"` and
an `extends:` key pointing at go-makefile's template. golangci-lint v2.12.2 has
no `extends` key (`golangci-lint config verify` rejects it). The gate works only
because `go.mk` passes `-c .make/golangci.yml`; a bare `golangci-lint run` in
that repo silently degrades to the 5 default linters. The go-makefile bootstrap
already warns to delete a per-repo `.golangci.yml`. This is a consumer
configuration issue, separate from the cache poisoning.

## Testing

- `internal/findings/findings_test.go` for `FindingPath`: a normal
  `path:line:col: msg` line returns the path; an absolute path returns it; a line
  with no `:line:col:` returns false.
- `extractFindings` test (with a temp worktree root holding a couple of real
  files): a local finding whose file exists is kept and normalized; a
  `../../phase1-shelldecomp/...` line whose file is absent is dropped; the dropped
  count is correct; a no-path line is kept.
- `lintEnv` test: the default sets `GOLANGCI_LINT_CACHE` to `.make/golangci-cache`;
  an existing value is respected.
- Regression: the `NormalizePath` awk oracle test still passes, since
  `NormalizePath` is unchanged.

## Live smoke validation

Unit tests prove the pure logic; a live smoke test proves the fix against a real
poisoned cache, run in isolation through `GO_MK_DEV_DIR`. Setting
`GO_MK_DEV_DIR=/Users/agoodkind/Sites/go-makefile` makes a consumer copy this
branch's `go.mk` and `golangci.yml` and build the go-mk binary from this checkout
(`GO_MK_BUILD_REPO` resolves to the dev dir), so the consumer runs this branch's
engine end to end with no push.

Reconstruct the poison with fabricated data:

1. Pick a consumer (agent-gate, or a minimal scaffolded module with one file that
   trips a default linter so a finding always exists).
2. Create a sibling git worktree `S` of the consumer at its current commit, so a
   target file is byte-identical across `S` and the main checkout.
3. In `S`, run golangci against the shared golangci cache (the default
   `~/Library/Caches/golangci-lint`), so the cache stores `S`'s absolute path
   with that file's issues.
4. Remove `S` (`git worktree remove`), reproducing the deleted-sibling condition.

Validate Component A (filter), forcing the shared poisoned cache so the filter is
actually exercised:

- In the consumer main checkout, run
  `GO_MK_DEV_DIR=<checkout> GOLANGCI_LINT_CACHE=<shared poisoned cache> make lint-golangci`.
- Expect: the findings under the removed `S` path are dropped, one warn line
  reports the dropped count, and the gate passes when no real new findings exist.
- Control: run the same with the current released engine (no `GO_MK_DEV_DIR`) and
  confirm it fails with the foreign findings, so the behavior change is
  attributable to the fix.

Validate Component B (isolation):

- In a fresh consumer worktree with `GO_MK_DEV_DIR=<checkout>` and no
  `GOLANGCI_LINT_CACHE` override, run `make lint-golangci` and confirm go-mk sets
  `GOLANGCI_LINT_CACHE` to `.make/golangci-cache`, so the run uses a per-worktree
  cache and never sees the shared poison.

If a genuinely poisoned `~/Library/Caches/golangci-lint` is still on hand, point
`GOLANGCI_LINT_CACHE` at it for the Component A step instead of reconstructing.
The original cache for this incident was already cleared, so fabrication is the
default path.

## Rollout

After the unit tests and both smoke validations pass on
`harden-golangci-cache-poisoning`:

1. Merge `harden-golangci-cache-poisoning` into `main`.
2. Push `main`.

Consumers pick up the new engine on their next run, since go-mk tracks the main
branch tip with no version pin.

## Files touched

- `internal/findings/findings.go` (new `FindingPath`)
- `internal/findings/findings_test.go` (tests)
- `cmd/go-mk/lint.go` (`extractFindings` existence guard and dropped count;
  `lintEnv` cache default)
- `cmd/go-mk/lint_gates.go` (golangci gate runners print the warn line when the
  dropped count is positive)
