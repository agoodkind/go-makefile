LIBRARY := 1
GO_MK_MODULES := go-build.mk
include .make/go.mk
.DEFAULT_GOAL := help

include recreated.mk
recreated.mk:
	printf 'ran\n' > recreated.mk

help:
	printf 'ran\n' > overridden
