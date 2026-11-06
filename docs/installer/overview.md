# Installer

go-makefile ships one root Go module. The `selfupdate` package lives at [selfupdate](../../selfupdate), and consumers import it as `goodkind.io/go-makefile/selfupdate` from the same module that builds [cmd/go-mk](../../cmd/go-mk) and [cmd/go-mk-install](../../cmd/go-mk-install).

## Binaries

`go-mk` is the build, lint, release, scaffold, and self-update engine. Its command tree includes `selfupdate`, `selfupdate watch`, and `selfupdate install-service` in [cmd/go-mk/selfupdate_command.go](../../cmd/go-mk/selfupdate_command.go). The one-shot command calls `selfupdate.RunUpdateCommand`; the watch command calls `selfupdate.RunScheduler`; the service command installs a launchd or systemd user service that runs `go-mk selfupdate watch`.

`go-mk-install` is the standalone first-install bootstrapper. [cmd/go-mk-install/main.go](../../cmd/go-mk-install/main.go) accepts `--repo OWNER/NAME`, `--binary NAME`, `--bin-dir DIR`, `--version TAG`, `--require-attestation`, and post-install arguments after `--`. It resolves the release tag through `selfupdate.ResolveReleaseTag`, installs through `selfupdate.InstallReleaseBinary`, and then execs the installed consumer binary when post-install arguments are present.

## Hosted Script

The hosted [install.sh](../../install.sh) fetches `go-mk-install_<os>_<arch>.tar.gz` and `checksums.txt` from the latest non-draft `agoodkind/go-makefile` release. It verifies the installer asset with `gh release verify-asset` when `gh` is available, falls back to SHA-256 verification when attestation is not required, extracts `go-mk-install`, and passes the consumer install request to it.

`GITHUB_TOKEN` is used for GitHub API and asset downloads. The same token is also exposed to `gh` as `GH_TOKEN` when the script verifies the release asset.

## Trust Chain

The first hop trusts the `go-mk-install` release asset from `agoodkind/go-makefile`. The hosted script verifies that asset with GitHub's stable release identity when possible and otherwise requires a checksum match from the same release. Passing `--require-attestation` makes the script fail instead of using the checksum fallback.

The installer uses `selfupdate` to download the consumer archive. It verifies the checksum and the signed build record in the Sigstore bundle. It extracts the binary into the requested directory and renames it over the target.

## Release Assets

Each release includes archives for `go-mk` and `go-mk-install`. The package workflow creates a Sigstore bundle for each archive, and the release publisher uploads both files.
