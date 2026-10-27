LIBRARY := 1
GO_MK_MODULES := go-build.mk
include .make/go.mk
.DEFAULT_GOAL := help

ifeq ($(MAKELEVEL),1)
help:
	printf 'ran\n' > overridden
endif
