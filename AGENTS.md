# go-makefile agent notes

Consumers fetch `go.mk` and its helper files from `main` at build time, so any
change here ships to every consumer's next build the moment it lands on `main`.

The reusable CI pipeline (prepare, compile and quality in parallel, then the
release dry run) is described in [docs/ci/overview.md](docs/ci/overview.md). The
grants and `secrets: inherit` a consumer's `ci.yml` must set to call it are in
[docs/ci/caller.md](docs/ci/caller.md).
