# go-makefile Agent Notes

## Downstream Validation

Before interpreting any downstream consumer result, explicitly check the
current `GO_MK_DEV_DIR` value.

- If `GO_MK_DEV_DIR` is set, treat that directory as the makefile source that
  consumers will use.
- If `GO_MK_DEV_DIR` is unset, treat the published `go-makefile/main` fetch
  path as the makefile source.
- Report which source was active before claiming whether a consumer used the
  latest makefile.

