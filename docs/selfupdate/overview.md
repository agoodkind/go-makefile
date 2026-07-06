# Self-update

go-makefile ships `selfupdate` as an importable package at [selfupdate](../../selfupdate) inside the root module. Consumers use it through the import path `goodkind.io/go-makefile/selfupdate`, and `go-mk` uses the same package for its own updater commands in [cmd/go-mk/selfupdate_command.go](../../cmd/go-mk/selfupdate_command.go).

## Package API

`Config`, `Options`, `Check`, `Apply`, and `RunUpdateCommand` live in [selfupdate/selfupdate.go](../../selfupdate/selfupdate.go) and [selfupdate/command.go](../../selfupdate/command.go). `Check` discovers the latest allowed release, selects the runtime archive named `<binary>_<os>_<arch>.tar.gz`, and records update state. `Apply` runs the same check under a lock, downloads the selected archive, verifies it, extracts the candidate, validates the candidate command, and replaces the installed binary.

`InstallReleaseBinary` and `ResolveReleaseTag` live in [selfupdate/install.go](../../selfupdate/install.go). `InstallReleaseBinary` is the first-install API used by [cmd/go-mk-install](../../cmd/go-mk-install). `ResolveReleaseTag` resolves an exact version or the latest rolling or stable channel without downloading an archive.

`VerifyReleaseAssets` lives in [selfupdate/release.go](../../selfupdate/release.go), and [selfupdate/cmd/verify-release](../../selfupdate/cmd/verify-release) exposes it as the post-publish verifier. The reusable release workflow calls that command when the caller passes a `binary` input in [.github/workflows/_release.yml](../../.github/workflows/_release.yml).

## Verification

Release verification checks the archive checksum and GitHub attestations. The checksum path is in [selfupdate/download.go](../../selfupdate/download.go) and [selfupdate/release.go](../../selfupdate/release.go). The release and build-provenance attestation checks are in [selfupdate/attestation.go](../../selfupdate/attestation.go).

Candidate validation runs the downloaded binary with `Config.ValidateArgs` and requires output containing `Config.ValidateMatch`. `go-mk` provides a real `version` command in [cmd/go-mk/selfupdate_command.go](../../cmd/go-mk/selfupdate_command.go), so its own updater validates a staged `go-mk` before replacement.

## Scheduler Service

`RunScheduler` in [selfupdate/scheduler.go](../../selfupdate/scheduler.go) runs a loop from `SchedulerHooks`. `Enabled` controls whether the loop acts, `Mode` selects check or apply, `Options` supplies the current config, and `StopForRelaunch` runs after an applied update so a service manager can restart the new binary.

The service helpers live in [selfupdate/service.go](../../selfupdate/service.go). `InstallLaunchdService` renders and loads a launchd user service. `InstallSystemdUserService` renders, enables, and restarts a systemd user service. `go-mk selfupdate install-service` calls the correct helper for the host platform and installs a user service that runs `go-mk selfupdate watch`.

## Build Identity

`Config` carries `CurrentVersion`, `CurrentCommit`, `CurrentBuildHash`, and `CurrentDirty`. `Config.validate` rejects an empty repo, binary, version, commit, or build hash. `go-mk` reads version fields from [internal/version](../../internal/version) and uses a runtime hash of the current executable when `BinHash` is empty.

`CurrentDirty` protects development builds. `Check` reports no available update when `CurrentDirty` is true, and `Apply` exits through the same no-op path because it calls `Check` first. [selfupdate/dirty_test.go](../../selfupdate/dirty_test.go) covers that guard.
