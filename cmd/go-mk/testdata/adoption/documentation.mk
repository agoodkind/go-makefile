GO_MK_URL := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK := .make/go.mk

documentation:
	@if curl -fsSL "$(GO_MK_URL)" -o "$@"; then \
		:; \
	fi

-include $(GO_MK)
