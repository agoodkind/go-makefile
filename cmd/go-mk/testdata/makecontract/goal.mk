LIBRARY := 1
GO_MK_MODULES := go-build.mk
include .make/go.mk
.DEFAULT_GOAL := help

ifneq ($(filter help,$(MAKECMDGOALS)),)
help:
	printf 'ran\n' > overridden
endif
