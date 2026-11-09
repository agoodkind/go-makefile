GO_MK_URL := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK := .make/go.mk

GO_MK_BOOTSTRAP := $(shell \
	mkdir -p .make; \
	if curl -fsSL "$(GO_MK_URL)" -o "$(GO_MK).tmp"; then \
		mv "$(GO_MK).tmp" "$(GO_MK)"; \
	fi)

$(GO_MK):
	@mkdir -p .make
	@if curl -fsSL "$(GO_MK_URL)" -o "$@"; then \
		:; \
	else \
		exit 1; \
	fi

-include $(GO_MK)
