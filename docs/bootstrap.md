# Bootstrap

A consumer sets its identity, then includes [bootstrap.mk](../bootstrap.mk). This repository holds the canonical copy. Each consumer commits its own.

The committed shim only obtains the helper. The helper owns validation, reuse, and failure for every local asset.

## Obtain the helper

Acquisition runs in this order:

1. Copy from a local go-makefile checkout when that checkout contains the helper.
2. Reuse a non-empty cached helper.
3. Download when neither path applies.

A warm checkout works offline. A cold start without network access fails at helper acquisition.

The helper follows the repository and ref the consumer pinned. Redirecting the engine URL does not change that.

## Provision assets

After the helper is present, the shim runs it. A non-zero exit stops the parse. The helper writes every required asset into the local cache.

## Use a pre-provisioned tree

Set `_GO_MK_PROVISIONED=1` when every required asset is already on disk and the parse must not touch the network. Helper acquisition is skipped. A non-empty helper must already be present. An empty file does not count as provisioned.

## Forward variables to the helper

Make does not export variables set on the command line or with a plain assignment in the consumer Makefile. The shim forwards every variable the helper reads. A variable the helper ignores is not forwarded. Adding a variable the helper reads means forwarding it from the shim too.

## Include modules

The engine includes optional modules after it defines its variables. The shim must not include modules. A duplicate include produces overriding-commands warnings.
