# bootstrap.mk: tiny shim that fetches go-makefile assets and includes them.
# Consumer Makefiles set their identity vars (BINARY, CMD, VPKG, MODULES, etc.)
# then `include bootstrap.mk`. Everything else (go.mk, golangci.yml, modules)
# is fetched at parse time and -included transitively.
#
# This file is canonical in agoodkind/go-makefile. Consumers commit a copy.
# Update path: edit go-makefile/bootstrap.mk, then refresh all consumer copies
# (one-off sync; not a long-term mechanism).

GO_MK_DEV_DIR  ?=
GO_MK_MODULES  ?=
GO_MK          := .make/go.mk
GO_MK_BASE_URL ?= https://raw.githubusercontent.com/agoodkind/go-makefile/main
GO_MK_API_REPO ?= agoodkind/go-makefile
GO_MK_API_REF  ?= main

# _go_mk_fetch and _go_mk_prime exist here because bootstrap.mk must fetch go.mk
# before any go.mk helpers are available. After go.mk is included, go.mk owns the
# sibling script/module/config fetches.
# Fetch order at parse time: dev override > files _go_mk_prime already extracted
# from one codeload tarball into .make/ > raw URL. The gh api contents path was
# removed: it spent one per-repo GITHUB_TOKEN core-REST call per file per job, and
# a single tarball costs zero core-REST.
# TODO(fetch-order): keep this order aligned with go.mk.
# TODO(moratorium): no on-disk cache fallback. _go_mk_prime removes each asset
# before re-copying, so a failed fetch falls through to raw or fails loud rather
# than serving a stale file.
define _go_mk_fetch
	if [ -n "$(GO_MK_DEV_DIR)" ] && [ -f "$(GO_MK_DEV_DIR)/$(1)" ]; then \
		cp "$(GO_MK_DEV_DIR)/$(1)" "$(2)"; \
	elif [ -s "$(2)" ]; then \
		: ; \
	elif curl -fsSL --connect-timeout 5 --max-time 10 --retry 3 --retry-delay 2 "$(GO_MK_BASE_URL)/$(1)" -o "$(2)" 2>/dev/null && [ -s "$(2)" ]; then \
		: ; \
	else \
		printf '%s\n' "error: $(1) fetch failed; no cache fallback (moratorium). Set GO_MK_DEV_DIR, or check network access to codeload.github.com and $(GO_MK_BASE_URL)" >&2; \
		exit 1; \
	fi
endef

# _go_mk_prime downloads the go-makefile archive once, extracts it into a throwaway
# temp dir, and copies only the assets this shim owns (go.mk, golangci.yml, and the
# GO_MK_MODULES) into .make/. It removes each asset first so a failed download never
# leaves a stale file for _go_mk_fetch to serve. Only .mk and .yml files land in
# .make/, so no go-makefile source pollutes the consumer's find-based lint targets.
define _go_mk_prime
	if [ -n "$(GO_MK_DEV_DIR)" ]; then \
		: ; \
	else \
		for asset in go.mk golangci.yml $(GO_MK_MODULES); do rm -f ".make/$$asset"; done; \
		tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/go-mk.XXXXXXXX") || exit 0; \
		if curl -fsSL --connect-timeout 5 --max-time 30 --retry 3 --retry-delay 2 "https://codeload.github.com/$(GO_MK_API_REPO)/tar.gz/$(GO_MK_API_REF)" 2>/dev/null | tar -xzf - -C "$$tmp" --strip-components 1 2>/dev/null; then \
			for asset in go.mk golangci.yml $(GO_MK_MODULES); do \
				if [ -f "$$tmp/$$asset" ]; then \
					mkdir -p "$$(dirname ".make/$$asset")"; \
					cp "$$tmp/$$asset" ".make/$$asset"; \
				fi; \
			done; \
		fi; \
		rm -rf "$$tmp"; \
	fi
endef

GO_MK_BOOTSTRAP_FETCHED := 1

define _go_mk_require_fetched
$(if $(wildcard $(1)),,$(error go-makefile expected $(1); rerun without GO_MK_SKIP_FETCH))
endef

ifeq ($(strip $(GO_MK_SKIP_FETCH)),1)
GO_MK_FETCH_CHECK := $(call _go_mk_require_fetched,$(GO_MK))
GO_MK_FETCH_CHECK += $(call _go_mk_require_fetched,.make/golangci.yml)
GO_MK_FETCH_CHECK += $(foreach m,$(GO_MK_MODULES),$(call _go_mk_require_fetched,.make/$(m)))
else

$(shell mkdir -p .make && { $(call _go_mk_prime); } 1>&2)
$(shell mkdir -p .make && { $(call _go_mk_fetch,go.mk,$(GO_MK)); } 1>&2)
$(shell { $(call _go_mk_fetch,golangci.yml,.make/golangci.yml); } 1>&2)
$(foreach m,$(GO_MK_MODULES),$(shell { $(call _go_mk_fetch,$(m),.make/$(m)); } 1>&2))

endif

# go.mk handles -including the modules at its tail (after all its variables
# are defined), so the modules see build-check etc. Don't duplicate
# the include here or every module target gets overriding-commands warnings.
-include $(GO_MK)
