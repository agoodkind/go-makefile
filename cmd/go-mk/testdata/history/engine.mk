ifneq ($(GO_MK_BIN),{{.}})
$(error historical preparation selected $(GO_MK_BIN) instead of {{.}})
endif
