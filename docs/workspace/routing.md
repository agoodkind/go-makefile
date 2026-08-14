# Workspace routing

go-makefile routes a dependency that the Go module proxy cannot build through a generated `go.work` file, which the build creates from the `GO_MK_WORKSPACE_USE` hook and never commits. The hook, the codegen it depends on, and the gitignore that keeps the file out of version control live in [go.mk](../../go.mk) and [cmd/go-mk/bootstrap.go](../../cmd/go-mk/bootstrap.go), and the consumer-facing summary lives in the [README](../../README.md).

## Why a workspace is needed

Some dependencies cannot build from the module proxy. The motivating case is `gksyntax`, whose generated tree-sitter parser C and nested grammar submodules are absent from the module zip the proxy serves. A plain `require` downloads that parser-less zip and fails to compile. The build instead points `go.work` at the initialized `third_party/gksyntax` submodule, so it compiles the real sources in place.

## Why the file is generated and gitignored

The policy is that `go.work` and `go.work.sum` stay gitignored, and the build regenerates `go.work` from `GO_MK_WORKSPACE_USE` on every run. The reason is reproducibility. The file is fully derived from the hook, so committing it adds drift surface without adding information. A committed `go.work.sum` needs hand-maintenance, and dependabot updates `go.mod` while leaving a committed `go.work` stale.

The common Go guidance against committing `go.work` targets two problems that do not apply here. It warns about absolute machine paths, while this `go.work` uses only relative paths. It warns about per-developer divergence for casual multi-module work, while this `go.work` is load-bearing rather than a convenience. The choice therefore rests on reproducibility, not on that guidance.

## Why not a go.mod replace

A committed relative `replace` in `go.mod` would also route the dependency, but the shared `gomoddirectives` lint rejects local replacements, so it cannot pass CI here. A generated workspace also keeps one coherent module graph through the `go mod` operations that dependabot performs, which a `replace` and workspace mix does not.

## The ordering invariant

The workspace generation must run after the codegen that initializes the submodule. `go work init` silently drops a use-path whose directory has no `go.mod` yet, so generating `go.work` before the submodule is present produces a file that omits the dependency. The symptom is that Go downloads the parser-less module from the proxy and the build fails on a missing `parser.c`. go.mk orders the workspace step after `GO_MK_GENERATE` to prevent this.

## Inherited go.work in nested worktrees

A worktree created inside a primary checkout can inherit that primary's `go.work` even when the worktree's own repo declares no workspace. Go discovers a `go.work` by walking up the filesystem from the current directory, so a worktree under a path such as `<primary>/.claude/worktrees/<name>/` finds the primary's `go.work` and turns workspace mode on. The primary's use-paths do not list the worktree's module, so every compile-bearing gate fails with `directory prefix . does not contain modules listed in go.work` and analyzers report skipped packages. This is an environment problem, not a code problem, but the raw error reads like a typecheck failure.

`go-mk-workspace` self-heals this case. Before any gate runs, it checks `go env GOWORK`. When a `go.work` is active from a parent directory and `go list ./...` fails, it writes a local `go work init .` that shadows the inherited workspace so the module resolves. The `go list ./...` probe reproduces the exact failing operation, so the shadow is written only when the inherited workspace is genuinely broken for this directory, never when it legitimately covers it. The self-heal runs for every repo, including those that set no `GO_MK_WORKSPACE_USE`, and is a no-op when no `go.work` is active. Set `GO_MK_WORKSPACE_NO_AUTOSHADOW=1` to print the cause instead of writing the shadow. A local `go.work` that already exists is always left untouched.

## Consumer requirements

A consumer with such a dependency sets `GO_MK_WORKSPACE_USE` to its workspace use-paths and `GO_MK_GENERATE` to its codegen target. It must not commit `go.work` or `go.work.sum`, which the bootstrap gitignores. It must not add `submodules: recursive` to its CI or release checkout, because a nested grammar submodule authenticates over `git@`, which fails on CI runners, and the codegen target initializes submodules over HTTPS instead.
