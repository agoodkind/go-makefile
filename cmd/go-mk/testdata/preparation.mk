override GO_MK_BIN := {{.Engine}}

.PHONY: notice-prepare-check
go-mk-workspace: notice-prepare-check

notice-prepare-check:
	test ! -f history-prepare-failure
