GO_MK_URL := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK := .make/go.mk

all:
	@echo "curl $(GO_MK_URL) -o $@"

-include $(GO_MK)
