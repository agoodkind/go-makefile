# Remove tracing from go-mk

The go-mk engine keeps ordinary structured logging but produces no traces,
trace identifiers, or trace headers.

## Problem

PR #133 moved provisioning into the go-mk engine. Logging setup runs before
command dispatch, so parse-time provisioning and the requested command each
create a trace and print a header.

The attempted repair correlates separate processes through process identifiers,
shared session files, locks, and platform-specific process inspection. That
machinery adds failure modes without improving the requested command's output.

## Decision

Remove tracing from go-mk completely.

The engine retains its per-concern JSONL logs and `GO_MK_LOG` summary modes.
Log records no longer require or contain `trace_id` or `span_id` fields.

Remove direct OpenTelemetry imports and requirements used only by tracing, trace
context propagation, correlation handlers, printed trace headers, process
inspection, trace session state, locks, and legacy trace state. Indirect
OpenTelemetry modules required by self-update and sigstore verification remain.

Capability probes bypass logging and run directly through `run()` to preserve
byte-exact machine output. Provisioning, binary resolution, user commands,
nested Make runs, and concurrent Make runs use the ordinary logging path. None
creates or joins a trace.

Consumers remain unchanged. Their committed bootstrap files and fetched Make
rules continue to invoke the same go-mk commands.

## Failure behavior

Logging initialization remains best effort, matching current behavior. A log
router or file failure must not change a command's exit status.

Removing tracing must not change command dispatch, provisioning, target output,
or structured log routing beyond the deleted trace fields and header line.

Old `.make/logs/.run` and `.make/logs/.traceparent` files are ignored. The engine
does not create, read, migrate, or delete them.

## Verification

Public command tests run representative commands and assert their observable
output contains no trace header or trace identifiers.

Logging tests verify that ordinary records still reach the expected summary and
per-concern JSONL outputs without trace fields.

Consumer validation covers helper provisioning, direct fetch, inline fetch,
nested Make, recursive Make, and concurrent Make entry points. Existing exit
statuses must remain unchanged, and no invocation may print a trace header.

Run `make check` and the Go race detector after focused tests pass.

## Removed components

- Trace and span creation.
- `TRACEPARENT` import, export, and persistence.
- Trace and span correlation fields.
- Trace header rendering.
- Process identifier and process ancestry inspection.
- Trace session files, cache directories, and locks.
- Legacy trace migration.
- Direct OpenTelemetry tracing imports and requirements used only by go-mk.
- Trace-specific unit and integration tests.

## Acceptance criteria

- No go-mk command prints a trace header.
- No go-mk log record contains `trace_id` or `span_id`.
- No go-mk command reads or writes trace state.
- Ordinary structured logs and `GO_MK_LOG` behavior remain available.
- Command output and exit status remain unchanged apart from removed trace data.
- No consumer repository changes are required.
- Focused tests, `make check`, the race detector, and consumer validation pass.
