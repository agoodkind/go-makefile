GO_MK_URL := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK := .make/go.mk

$(GO_MK):
	@mkdir -p .make
	@if curl -fsSL "$(GO_MK_URL)" -o "$@"; then \
		:; \
	else \
		exit 1; \
	fi

-include $(GO_MK)
