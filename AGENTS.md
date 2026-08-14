# go-makefile agent notes

Unpinned consumers fetch the rolling GitHub Release (`go-makefile-src.tar.gz` plus a signed `go-mk` binary) at parse time. `GO_MK_DEV_DIR` still wins for local engine work. An explicit `GO_MK_API_REF` other than `main` pins a git ref and falls back to codeload when that ref has no release asset yet.

The reusable CI pipeline (prepare, compile and quality in parallel, then the
release dry run) is described in [docs/ci/overview.md](docs/ci/overview.md). The
grants and `secrets: inherit` a consumer's `ci.yml` must set to call it are in
[docs/ci/caller.md](docs/ci/caller.md).
