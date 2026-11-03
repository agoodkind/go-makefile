LIBRARY := 1
GO_MK_MODULES := go-build.mk
GO_MK_GENERATE := generate-input
GO_MK_CGO_DEPS := example
include .make/go.mk
.DEFAULT_GOAL := help

help: added-prerequisite

added-prerequisite:
	printf 'ran\n' > prerequisite-ran

generate-input:
	printf 'ran\n' > generator-ran

go-mk-cgo-dep-example:
	printf 'ran\n' > cgo-hook-ran

custom:
	@:

custom:
	printf 'ran\n' > custom-ran
