SETTING ?= enabled
export SETTING
LIBRARY := 1
GO_MK_MODULES := go-build.mk
include .make/go.mk
.DEFAULT_GOAL := help

ifeq ($(origin SETTING),file)
help:
	printf 'ran\n' > overridden
endif
