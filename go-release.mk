# go-release.mk: opt-in release target. The whole pipeline (cross-compile with
# CGO disabled, anchore/quill sign + notarize for darwin, tar.gz with sha256
# checksums, tag push, GitHub release) lives in the go-mk binary `release`
# command, so there is no shell script. This module wires the target and exports
# the inputs the command reads.
#
# Inputs (from go-build.mk and the project Makefile):
#   BINARY, CMD, VPKG, GKLOG_VPKG   build identity and version stamping
#   RELEASE_BINS                    full name:cmd set to release (default BINARY:CMD; must include BINARY)
#   RELEASE_PLATFORMS               os/arch list (default darwin+linux, amd64+arm64)
#   RELEASE_ENTITLEMENTS            optional entitlements XML for darwin signing
#   REQUIRE_DARWIN_CODESIGN         fail darwin release builds when signing
#                                   material is absent
#   DIST_DIR                        output directory (default dist)
#
# Credentials are read by quill from QUILL_SIGN_P12, QUILL_SIGN_PASSWORD,
# QUILL_NOTARY_KEY, QUILL_NOTARY_KEY_ID, QUILL_NOTARY_ISSUER. Signing is skipped
# when QUILL_SIGN_P12 is empty, so snapshot builds need no credentials.

.PHONY: release

RELEASE_PLATFORMS    ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64
RELEASE_BINS         ?=
RELEASE_ENTITLEMENTS ?=
REQUIRE_DARWIN_CODESIGN ?=

export BINARY
export CMD
export VPKG
export GKLOG_VPKG
export DIST_DIR
export RELEASE_BINS
export RELEASE_PLATFORMS
export RELEASE_ENTITLEMENTS
export REQUIRE_DARWIN_CODESIGN

release: | go-mk-bin
	@"$(GO_MK_BIN_RESOLVED)" release

# GO_MK_PREREQS (see go.mk): codegen and go.work routing. Only the stages that
# compile the module (build, or the single-runner all-in-one pipeline with no
# stage) need generated parsers/proto and go.work. The tag and publish stages
# never compile, and a consumer whose generate hook needs platform-specific
# tools (macOS xcrun/lipo/swiftc) must be able to tag and publish from the
# Ubuntu runners. Empty default is a no-op.
ifneq ($(strip $(GO_MK_PREREQS)),)
ifeq ($(filter tag publish,$(RELEASE_STAGE)),)
release: | $(GO_MK_PREREQS)
endif
endif
