# Bootstrap

A consumer sets identity variables such as `BINARY`, `CMD`, `VPKG`, and `GO_MK_MODULES`, then includes [bootstrap.mk](../bootstrap.mk). This repository holds the canonical copy. Each consumer commits its own.

The committed file only obtains the bootstrap helper. The helper owns validation, reuse, and failure for every asset under `.make/`.

## Obtain the helper

Acquisition runs in this order:

1. Copy from `GO_MK_DEV_DIR` when that directory contains the helper script.
2. Reuse a non-empty cached helper.
3. Download with curl when neither path applies.

A warm checkout works offline. A cold start without network access fails at helper acquisition.

The helper URL follows `GO_MK_API_REPO` and `GO_MK_API_REF`. `GO_MK_BOOTSTRAP_BASE_URL` is a test override. `GO_MK_BASE_URL` is not used here because it is pinned to `main`.

## Provision assets

After the helper is present, the committed file runs it. A non-zero exit stops the parse. The helper writes every required asset into `.make/` and sets `GO_MK_PROVISION` to `ok` on success.

## Use a pre-provisioned tree

Set `_GO_MK_PROVISIONED=1` when every required asset is already on disk and the parse must not touch the network. Helper acquisition is skipped. A non-empty helper must already be present. An empty file does not count as provisioned.

## Forward variables to the helper

Make does not export variables set on the command line or with a plain assignment in the consumer Makefile. The committed file forwards every variable the helper reads:

- `GO_MK_API_REPO`
- `GO_MK_API_REF`
- `GO_MK_MODULES`
- `GO_MK_CODELOAD_BASE`
- `GO_MK_DEV_DIR`
- `_GO_MK_PROVISIONED`

`GO_MK_BASE_URL` and `GO_MK_BOOTSTRAP_BASE_URL` are not forwarded. The helper never reads them. Adding a variable the helper reads means adding it to that forward list.

## Include modules

The engine includes optional modules after it defines its variables. The committed file must not include modules. A duplicate include produces overriding-commands warnings.
