# Cgo dependency provisioning

go-makefile builds a consumer's external C libraries per build target through the `GO_MK_CGO_DEPS` hook, so cgo consumers cross-compile with the same declaration that drives their host builds. The hook, its per-target environment, and the toolchain resolution live in [go.mk](../../go.mk); the release engine's invocation lives in [cmd/go-mk/release.go](../../cmd/go-mk/release.go); the workflow that publishes the cross toolchain lives in [.github/workflows/_release_build.yml](../../.github/workflows/_release_build.yml).

## The consumer contract

A consumer sets `GO_MK_CGO_DEPS` to its dependency names before `include bootstrap.mk` and defines one `go-mk-cgo-dep-<dep>` target per name. The recipe builds the library into `GO_MK_CGO_PREFIX` and installs a pkg-config `.pc` file under `$(GO_MK_CGO_PREFIX)/lib/pkgconfig`. The recipe reads plain `$CC` and `$CXX` for the compiler and `GO_MK_TARGET_GOOS` / `GO_MK_TARGET_GOARCH` for the target. go-makefile names no specific library; only the consumer does.

The declaration activates everything else. go.mk runs the hook before every compile-bearing target through `GO_MK_PREREQS`, and exports `PKG_CONFIG_PATH` so host gates resolve the provisioned `.pc` files. A dep recipe that needs generated inputs first declares its own ordering (`go-mk-cgo-dep-x: | $(GO_MK_GENERATE)`).

## The two invocation contexts

The hook runs from two entry points, and both provide the same environment contract.

The release engine provisions each platform before its `go build`: `provisionCgoDeps` in [cmd/go-mk/release.go](../../cmd/go-mk/release.go) runs `make go-mk-cgo-deps` with the target tuple, the per-target prefix, and the resolved compiler.

The make layer runs the same hook as an order-only prerequisite of build, vet, lint, test, install, and the compile-bearing release stages, through `GO_MK_PREREQS` in [go.mk](../../go.mk). The release target attaches the hook only when the stage compiles (the build stage, or the stage-less all-in-one pipeline); the tag and publish stages skip it, so a consumer whose prerequisites need platform-specific tools still tags and publishes from any runner (see [go-release.mk](../../go-release.mk)). The hook's target-specific exports supply the tuple, the prefix, `PKG_CONFIG_PATH`, and the compiler there as well.

## How the toolchain flows

The release workflow publishes the target declaration job-wide: [\_release_build.yml](../../.github/workflows/_release_build.yml) writes `GO_MK_TARGET_GOOS` / `GO_MK_TARGET_GOARCH` for every cgo job and `GO_MK_CC` / `GO_MK_CXX` for the darwin cross jobs. The cross compiler travels under dedicated names, not global `CC` / `CXX`, so host tools in the same job (the go-mk binary, quill) keep the host compiler.

go.mk resolves `GO_MK_CC` / `GO_MK_CXX` into `CC` / `CXX` at the hook, so a dep recipe compiles with the target's compiler no matter which entry point invoked it. The release engine also injects the resolved `CC` / `CXX` into its own hook invocation (`crossCompilerEnv` in [cmd/go-mk/release.go](../../cmd/go-mk/release.go)); both mechanisms read the same job-wide variables, so they agree. Unset `GO_MK_CC` / `GO_MK_CXX` leave `CC` / `CXX` passing through unchanged, which preserves a linux job's multi-word `CC="ccache gcc"`.

`GO_MK_CGO_PREFIX` keys the install prefix by os/arch so a darwin cross build and a linux native build never share artifacts. An empty target tuple (a plain host build) falls back to the os/arch that `go build` targets by default.

## Caching provisioned dependencies

The release build caches each target's `GO_MK_CGO_PREFIX` so a warm run skips the dependency build. The `go-mk cache-manifest` command in [cmd/go-mk/cachemanifest.go](../../cmd/go-mk/cachemanifest.go) emits a per-target `cgo_cache_key`, and [\_release_build.yml](../../.github/workflows/_release_build.yml) restores and saves the prefix under that exact key. On a cache hit `provisionCgoDeps` in [cmd/go-mk/release.go](../../cmd/go-mk/release.go) reads a stamp file in the prefix and skips `make go-mk-cgo-deps`.

A consumer strengthens the key with two optional variables set before `include bootstrap.mk`. `GO_MK_CGO_CACHE_VERSIONS` lists `dep=version` pairs so a version bump invalidates the cache, for example `GO_MK_CGO_CACHE_VERSIONS := pcre2=10.45`. `GO_MK_CGO_CACHE_INPUTS` lists the recipe's build-script paths so editing a script invalidates the cache. Both default to empty, which leaves the key based on the dep list, the target tuple, the resolved compiler, and the tracked Makefile and `.mk` files.

## Sources of truth

The hermetic fixture tests in [cmd/go-mk/releasecgohook_test.go](../../cmd/go-mk/releasecgohook_test.go) enforce the environment contract: the compiler resolution the recipe observes and the prerequisite fold-in. [cmd/go-mk/release_test.go](../../cmd/go-mk/release_test.go) enforces the release engine's provisioning environment and the no-op guarantee for consumers without declared deps.
