# Add good and bad examples for lint rules

Add a built-in `make help-examples` command that explains each lint rule with rejected code and an accepted replacement.

## Behavior

Print available rule identifiers and short explanations by default. Do not print code examples until a filter selects one rule.

Use `LINTER=...` to select a tool and `RULE=...` to narrow rule identifiers. Prefer exact identifiers, including qualified `tool/id` identifiers. Otherwise, match partial rule identifiers without regard to case. List the remaining choices when several rules match.

After selection identifies one rule, print its identifier, a short explanation, a `Bad` example, and a `Good` example. Examples must show the same operation before and after the correction. Include enough context to explain conditions such as production code versus test code.

Cover the rules enabled by go-makefile's shared lint configuration, including staticcheck-extra analyzers, golangci-lint checks, formatters, gocyclo, and deadcode. Use diagnostic identifiers for tools that expose individual rules. A single example for a linter with several distinct rules does not satisfy coverage.

A filter that matches nothing fails with a clear error and explains how to print the available rules. Print choices in a stable order, grouped by tool and sorted by identifier.

## Approaches

The user chose a built-in catalog that lists possible examples before narrowing the choices. Printing all code examples by default prevents the requested selection workflow.

The agent recommends embedding the catalog in the existing Go engine and exposing it through the shared Make target. Fetching examples from external documentation during command execution would require network access and would not provide built-in examples.

## Constraints and agent decisions

The agent selects plain text output with consistent indentation. The command prints examples without running lint tools or executing example code. After normal engine provisioning, the catalog does not require additional downloads or tool installation.

The catalog covers the shared configuration shipped with the engine. Discovering consumer-specific rules or interpreting local configuration overrides falls outside this change.

Use one catalog for the Make target and the engine command. Add the target to `make help` and expose it in this repository and consumer repositories.

## Verification

Run the Make target to verify the initial list, tool filtering, partial rule filtering, selection of one rule, and failure for an unknown identifier. Verify that listings omit code examples.

Review examples against the configured rule implementations. The bad example must demonstrate its stated rule, and the good example must correct that finding. Compare catalog coverage with the shared configuration and custom analyzer registration.

Do not add feature tests or validation harnesses. Use direct command checks and source inspection for this feature.

Run the repository's required build checks before committing implementation changes. Do not change lint enforcement, exclusions, or baselines to accommodate examples.
