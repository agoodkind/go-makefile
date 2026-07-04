# Self-update

go-makefile ships the `selfupdate` library so a consumer binary can discover, verify, and install its own newer release, and so a consumer daemon can do the same on a timer. The public surface (`Config`, `Options`, `Check`, `Apply`, `RunScheduler`, `LoadState`, `VerifyReleaseAssets`) lives in [selfupdate/selfupdate.go](../../selfupdate/selfupdate.go); release discovery and verification live in [selfupdate/release.go](../../selfupdate/release.go); the scheduler lives in [selfupdate/scheduler.go](../../selfupdate/scheduler.go). The library never relaunches a process itself; the caller owns relaunch through the scheduler's `StopForRelaunch` hook or its own update command.

## The update decision

`Check` in [selfupdate/selfupdate.go](../../selfupdate/selfupdate.go) fetches the latest allowed release, selects the archive asset named `<binary>_<os>_<arch>.tar.gz` for the running platform, and reports `UpdateAvailable`. It computes availability as `!CurrentDirty && releaseIsNewer(CurrentVersion, LatestTag)`, so a dirty build is never reported as updatable even when a newer release exists. `Apply` calls `Check` inside a lock and returns a no-op result when `UpdateAvailable` is false, so the same guard stops both a manual apply and the scheduler's apply.

`releaseIsNewer` in [selfupdate/release.go](../../selfupdate/release.go) compares two versions. It uses `semver.Compare` when both are valid semver, and otherwise compares the leading twelve-digit timestamp of each version string, which is the prefix that go-makefile stamps into release tags. The exact ordering rules are the function's own; read it rather than restating them here.

## Build identity

`Config` carries the running binary's identity: `CurrentVersion`, `CurrentCommit`, `CurrentBuildHash`, and `CurrentDirty`. `Config.validate` in [selfupdate/selfupdate.go](../../selfupdate/selfupdate.go) rejects an empty `Repo`, `Binary`, `CurrentVersion`, `CurrentCommit`, or `CurrentBuildHash`, so a consumer that leaves the build hash empty fails every check and apply with `current build hash is required`.

`CurrentBuildHash` must come from a value that is populated in every build, including a local `make deploy`. The gklog `BinHash` variable is not that value: a binary content hash cannot be computed at link time, so [go-build.mk](../../go-build.mk) stamps `GKLOG_VPKG.BinHash=` empty for local builds and [cmd/go-mk/release.go](../../cmd/go-mk/release.go) stamps it empty for release builds too. A consumer therefore sources `CurrentBuildHash` from a runtime hash of the on-disk executable (for example `gklog/version.BuildHash()`), which is always populated.

`CurrentDirty` marks a dev or locally-built binary. [go-build.mk](../../go-build.mk) stamps `GKLOG_VPKG.Dirty` from `git diff --quiet`, so a consumer sets `CurrentDirty` from that stamped value (for example `gklog/version.Dirty == "true"`). The release engine stamps `Dirty=false`, so a real release is never treated as dirty. `Check` surfaces the guard through `CheckResult.DevBuild` so a consumer's `update check` output can distinguish a suppressed dev build from a genuinely current one.

## Verification and install

`Apply` downloads the asset, verifies it, extracts the candidate, validates it, and replaces the installed binary, all in [selfupdate/selfupdate.go](../../selfupdate/selfupdate.go) and [selfupdate/release.go](../../selfupdate/release.go). Verification checks the SHA-256 against the release `checksums.txt` and then verifies GitHub attestations against the release build workflow (see [selfupdate/attestation.go](../../selfupdate/attestation.go)). Candidate validation runs the downloaded binary with `Config.ValidateArgs` and requires its output to contain `Config.ValidateMatch`, so a binary that does not identify itself as the expected tool is never installed.

The generated installer performs the same verification for a first install. [cmd/go-mk/bootstrap_assets/install.sh.tmpl](../../cmd/go-mk/bootstrap_assets/install.sh.tmpl) downloads the archive and `checksums.txt`, verifies the SHA-256, verifies the GitHub attestation when `gh` is available, and installs the binary atomically. The post-publish `verify` job in [.github/workflows/_release.yml](../../.github/workflows/_release.yml) runs `selfupdate/cmd/verify-release` against the just-published release; it is gated on `inputs.binary != ''`, so a consumer activates it by passing `binary: <name>` to the reusable release workflow.

## The daemon scheduler

`RunScheduler` in [selfupdate/scheduler.go](../../selfupdate/scheduler.go) runs a consumer daemon's update loop from `SchedulerHooks`: `Enabled` gates the loop, `Mode` selects check or apply, `Options` supplies the per-tick config, and `StopForRelaunch` runs after a successful apply so the caller can exit and let its service manager relaunch the new binary. Because `Check` forces `UpdateAvailable` false for a dirty build, an apply-mode scheduler in a dev build takes no action, so a `make deploy` binary is not replaced by a release while it runs.

## Sources of truth

[selfupdate/dirty_test.go](../../selfupdate/dirty_test.go) enforces the dev-build guard: it asserts that a clean build reports an available update and that an otherwise identical dirty build reports none with `DevBuild` set. The remaining behavior (asset selection, checksum and attestation verification, candidate validation, state persistence, and per-iteration scheduler panic recovery) is covered by the other tests in the [selfupdate](../../selfupdate) package.
