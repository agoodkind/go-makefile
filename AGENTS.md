# go-makefile agent notes

Unpinned consumers adopt this repository's main at parse time, so a landing
here ships on their next build. A cold parse needs Go on PATH to obtain the
engine. A local checkout override still wins. An air-gapped run reuses a
pre-provisioned tree.

The reusable CI pipeline (prepare, compile and quality in parallel, then the
release dry run) is described in [docs/ci/overview.md](docs/ci/overview.md). The
grants and `secrets: inherit` a consumer's `ci.yml` must set to call it are in
[docs/ci/caller.md](docs/ci/caller.md).
