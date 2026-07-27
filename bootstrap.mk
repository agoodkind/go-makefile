# bootstrap.mk: tiny shim that obtains the go-makefile bootstrap helper, runs
# it, and includes the engine. Consumer Makefiles set their identity vars
# (BINARY, CMD, VPKG, MODULES, etc.) then `include bootstrap.mk`.
#
# This file is canonical in agoodkind/go-makefile. Consumers commit a copy.
# It deliberately holds no fetch policy beyond obtaining the helper: the helper
# is fetched, so a change to validation, reuse, or failure behavior reaches
# every consumer on its next run with no consumer-side change.

GO_MK_DEV_DIR  ?=
GO_MK_MODULES  ?=
GO_MK          := .make/go.mk
GO_MK_BASE_URL ?= https://raw.githubusercontent.com/agoodkind/go-makefile/main
GO_MK_API_REPO ?= agoodkind/go-makefile
GO_MK_API_REF  ?= main

GO_MK_BOOTSTRAP := .make/scripts/go-mk-bootstrap.sh
# The helper URL follows GO_MK_API_REF so a ref-pinned consumer gets that ref's
# helper. GO_MK_BASE_URL ends in /main and would pin the helper to main, so it
# is not used here.
GO_MK_BOOTSTRAP_URL := https://raw.githubusercontent.com/$(GO_MK_API_REPO)/$(GO_MK_API_REF)/scripts/go-mk-bootstrap.sh

# Obtaining the helper is the only fetch rule left in consumer-committed code.
# It never removes an existing helper, so a warm checkout stays usable when the
# network is gone, and only a cold offline start fails here.
define _go_mk_get_bootstrap
	if [ -n "$(GO_MK_DEV_DIR)" ] && [ -f "$(GO_MK_DEV_DIR)/scripts/go-mk-bootstrap.sh" ]; then \
		mkdir -p .make/scripts; \
		cp "$(GO_MK_DEV_DIR)/scripts/go-mk-bootstrap.sh" "$(GO_MK_BOOTSTRAP)"; \
	elif [ -s "$(GO_MK_BOOTSTRAP)" ]; then \
		: ; \
	else \
		mkdir -p .make/scripts; \
		tmp=$$(mktemp "$(GO_MK_BOOTSTRAP).tmp.XXXXXX") || exit 1; \
		if curl -fsSL --connect-timeout 5 --max-time 10 --retry 3 --retry-delay 2 \
			"$(GO_MK_BOOTSTRAP_URL)" -o "$$tmp" 2>/dev/null && [ -s "$$tmp" ]; then \
			mv "$$tmp" "$(GO_MK_BOOTSTRAP)"; \
		else \
			rm -f "$$tmp"; \
			printf '%s\n' "error: could not obtain $(GO_MK_BOOTSTRAP). Set GO_MK_DEV_DIR, or check network access to raw.githubusercontent.com" >&2; \
			exit 1; \
		fi; \
	fi; \
	chmod +x "$(GO_MK_BOOTSTRAP)"
endef

GO_MK_BOOTSTRAP_FETCHED := 1

ifeq ($(strip $(GO_MK_SKIP_FETCH)),1)
$(if $(wildcard $(GO_MK_BOOTSTRAP)),,$(error go-makefile expected $(GO_MK_BOOTSTRAP); rerun without GO_MK_SKIP_FETCH))
else
$(shell { $(call _go_mk_get_bootstrap); } 1>&2)
endif

# The helper provisions every asset and owns the validation, reuse, and failure
# rules. A non-zero exit means it could not produce a usable .make, so stop
# rather than parse an engine that is not there.
#
# GO_MK_API_REPO/GO_MK_API_REF are passed explicitly rather than relied on to
# reach the helper's environment implicitly: Make only auto-exports variables
# that originated from the process environment, not ones a consumer set with
# a plain assignment inside their own Makefile before this include. Without
# this, a consumer who pins GO_MK_API_REF that way would still get the right
# ref for the helper script itself (GO_MK_BOOTSTRAP_URL substitutes it at the
# Make level, not through the environment) but the helper would silently fall
# back to its own main/agoodkind/go-makefile defaults once running, fetching
# the wrong ref's assets.
GO_MK_PROVISION := $(shell GO_MK_API_REPO="$(GO_MK_API_REPO)" GO_MK_API_REF="$(GO_MK_API_REF)" GO_MK_MODULES="$(GO_MK_MODULES)" bash "$(GO_MK_BOOTSTRAP)" >&2 && printf ok)
$(if $(filter ok,$(GO_MK_PROVISION)),,$(error go-makefile failed to provision its assets))

# go.mk handles -including the modules at its tail (after all its variables
# are defined), so the modules see build-check etc. Don't duplicate
# the include here or every module target gets overriding-commands warnings.
-include $(GO_MK)
