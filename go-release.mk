# go-release.mk: standardized goreleaser wrapper with mandatory codesign +
# notarize for darwin builds.
#
# Project must have:
#   .goreleaser.yaml at repo root. The canonical template is rendered into the
#     repo by bootstrap.sh from go-makefile/templates/goreleaser.yaml.tmpl.
#     The template wires GoReleaser v2 native notarize support to env vars
#     resolved via 1Password (notarize.env).
#   notarize.env at repo root (gitignored). Copy notarize.env.example from
#     go-makefile and fill in op:// paths for the Developer ID cert,
#     password, and notarytool API key.
#
# Targets:
#   release-snapshot  Local snapshot, no publish, no notarize. Smoke test.
#   release-local     Full release with notarization. Requires notarize.env.
#   release           Alias for release-local. Used by CI on tag push.

.PHONY: release release-local release-snapshot release-check

GORELEASER   ?= goreleaser
NOTARIZE_ENV ?= notarize.env

release-snapshot:
	@GOFLAGS= $(GORELEASER) release --snapshot --clean --skip=publish --skip=notarize

release-local: release-check
	@GOFLAGS= op run --env-file=$(NOTARIZE_ENV) -- $(GORELEASER) release --clean

release: release-local

release-check:
	@if [ ! -f .goreleaser.yaml ] && [ ! -f .goreleaser.yml ]; then \
		echo "release: .goreleaser.yaml not found in repo root" >&2; \
		echo "  run bootstrap.sh to render the canonical template" >&2; \
		exit 1; \
	fi
	@if [ ! -f $(NOTARIZE_ENV) ]; then \
		echo "release: $(NOTARIZE_ENV) not found" >&2; \
		echo "  copy notarize.env.example from go-makefile and fill in your 1Password op:// paths" >&2; \
		exit 1; \
	fi
	@command -v op >/dev/null 2>&1 || { echo "release: 1Password CLI 'op' not found" >&2; exit 1; }
	@command -v $(GORELEASER) >/dev/null 2>&1 || { echo "release: $(GORELEASER) not found" >&2; exit 1; }
