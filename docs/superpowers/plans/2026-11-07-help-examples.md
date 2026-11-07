# Help examples implementation plan

> Use `executing-plans` to implement the plan task by task. Track steps with checkbox syntax.

**Goal:** List available rule examples, narrow the choices with `LINTER` and `RULE`, and print good and bad code only after selecting one rule.

**Architecture:** Embed JSON catalogs in the existing Go engine. Register `help-examples` as an engine command and expose it through the shared Make target and this repository's wrapper.

**Tech Stack:** Go, Cobra, GNU Make, and the repository's configured lint tools.

**Spec:** [Approved design](../specs/2026-11-07-help-examples-design.md).

## Global constraints

Keep lint configuration, enforcement, and baselines unchanged.
Use the configured tool versions and shared defaults when determining coverage.
Print examples without running linters, installing tools, or fetching documentation.
Use one catalog for direct engine and Make invocations.
Preserve exact diagnostic identifiers and fail for unknown identifiers.
Add neither feature tests nor validation harnesses, as the user requested.

## Review focus

Overlapping identifiers from different tools must list the remaining choices and accept a qualified identifier.
Make command-line values must preserve `RULE` without interpreting it as shell code.
An empty selector must list every rule in stable tool and identifier order without printing code.
Help must remain available when a consumer declares generation, workspace, or C dependency prerequisites.
The catalog must cover individual enabled diagnostic identifiers rather than only linter families.

## File responsibilities and review boundary

Add the catalog loader and renderer in [cmd/go-mk/help_examples.go](../../../cmd/go-mk/help_examples.go). Store entries under the adjacent `help_examples` directory in separate JSON files for custom analyzers, upstream rule families, and other linters. Each entry has string fields `tool`, `id`, `summary`, `bad`, and `good`.

Register the engine command in [cmd/go-mk/cli.go](../../../cmd/go-mk/cli.go). Add the shared target and help listing in [go.mk](../../../go.mk), and delegate once from the repository [Makefile](../../../Makefile).

Keep this feature in one pull request because the command, catalog, and consumer target jointly implement the approved behavior. Independent catalog research and edits can use bounded subagents.

## Task 1: Inventory and populate the catalog

Consumes the shared configuration, custom analyzer registration, and configured upstream tool sources.
Produces embedded JSON entries with complete rule coverage.

- [ ] Enumerate enabled linters and named subrules from the configured versions. Exclude disabled checks.
- [ ] Add paired examples for custom analyzers, upstream named checks, and the remaining enabled linters and formatters.
- [ ] Review pairs against the configured analyzer implementations and correct concrete errors.
- [ ] Commit the catalog with the command after Task 2 verification.

## Task 2: Add the command and Make target

Consumes the JSON catalog from Task 1.
Produces `runHelpExamples() int`, registered as `go-mk help-examples`, and `make help-examples`.

- [ ] Load embedded entries into a concrete `ruleExample` type and validate required fields and duplicate qualified identifiers.
- [ ] Filter tools with `LINTER` and rule identifiers with `RULE`. Prefer exact matches and otherwise match partial rule identifiers without regard to case. Accept `tool/id` for qualification and reject filters that match nothing.
- [ ] List rule identifiers and explanations until one filtered rule remains. Then print its indented `Bad` and `Good` blocks. Sort choices by tool and identifier.
- [ ] Register the command using the existing no-argument engine command pattern. Exported `RULE` already supplies Make selection.
- [ ] Add a phony shared target that depends only on engine resolution. Add the target to `make help` and the repository wrapper.
- [ ] Inspect actual output from initial, tool-filtered, partially filtered, qualified, ambiguous, and unknown rule invocations.

## Task 3: Verify and publish for review

Consumes the complete feature and approved specification.
Produces a ready-for-review pull request with verified command behavior.

- [ ] Compare catalog identifiers against the inventory and resolve every missing example.
- [ ] Run the repository's `make build` checks. The repository explicitly rejects `make check`.
- [ ] Inspect direct command output, complete Make output, filtered Make output, and unknown-rule exit status.
- [ ] Review the final diff against the specification and commit coherent implementation changes.
- [ ] Create a ready-for-review pull request. Preserve the active worktree and branch while review remains open.
