# Lint policy implementation plan

> Use executing-plans for implementation. Track completion with checkboxes.

Goal: Migrate struct initialization checks, enable compatible checks, and check
test files through three dependent pull requests with applied notices.

Architecture: Retain the pinned toolchain and existing notice mechanism. Each
layer changes the shared configuration and introduces only its new finding
scopes. Test coverage also requires removing the engine's default test filter.

Tech stack: Go, golangci-lint, Make, and gh-stack.

## Constraints

Preserve inherited work outside this stack.
Keep existing analyzer APIs, initialization registration, and successful
analysis callbacks compatible.
Preserve external JSON and YAML tag contracts.
Use effective-date applied notices for every layer.
Preserve the reproduced cgo duplicate-expression exception.

## Review focus

Explicit blank assignments must remain valid under errcheck.
Interface methods must retain their required external return types.
Struct initialization checks must preserve the intended internal type scope.
Test-file findings must survive both configuration and engine filtering.
Notices must preserve unrelated baseline rows and reject later findings.

## Layer 1: Migrate struct initialization checks

Branch: codex/lint-policy/exhaustruct-v5, based on main.

- [x] Replace exhaustruct with exhaustruct_v5 in explicit mode.
- [x] Preserve the internal type-selection regex and enable embedlit.
- [x] Add notices for v5 findings and embedlit findings.
- [x] Verify internal, external, empty, aliased, and promoted literals.
- [x] Commit the layer and rebase the remaining stack.

## Layer 2: Enable compatible checks

Branch: codex/lint-policy/checks, based on layer 1.

- [x] Update [golangci.yml](../../../golangci.yml) from the audit.
- [x] Add narrowly scoped records to [notices.txt](../../../notices.txt).
- [x] Verify the actual enabled list and schema with the pinned executable.
- [x] Exercise check findings and applied notices through a real consumer.
- [x] Commit the layer and rebase the remaining stack.

## Layer 3: Check test files

Branch: codex/lint-policy/tests, based on layer 2.

- [x] Remove the blanket test exclusion from configuration and Make defaults.
- [x] Remove the fallback in the engine's golangci exclusion helper.
- [x] Retain only exceptions that actual test contracts justify.
- [x] Add a notice restricted to test-file findings.
- [x] Verify historical test adoption, later test failures, and production scope.
- [x] Run simplify reviews, relevant checks, and publish the three ready PRs.
