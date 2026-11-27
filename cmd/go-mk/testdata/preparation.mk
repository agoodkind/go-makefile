ifneq ($(wildcard history-prepare-failure),)
$(error Historical preparation failed)
endif

.PHONY: go-mk-workspace go-mk-cgo-deps
go-mk-workspace go-mk-cgo-deps:
