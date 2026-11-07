# Add good and bad examples for lint rules

Add a built-in `make help-examples` command that explains each lint rule with rejected code and an accepted replacement.

## Behavior

The user selected printing every rule by default and allowing `RULE=...` to select one rule.

Each entry prints the rule identifier, a short explanation, a `Bad` example, and a `Good` example. Examples must show the same operation before and after the correction. Include enough context to explain conditions such as production code versus test code.

Cover the rules enabled by go-makefile's shared lint configuration, including staticcheck-extra analyzers, golangci-lint checks, formatters, gocyclo, and deadcode. Use diagnostic identifiers for tools that expose individual rules. A single example for a linter with several distinct rules does not satisfy coverage.

An unknown `RULE` fails with a clear error and explains how to print the available rules. Print entries in a stable order, grouped by tool and sorted by identifier.

## Approaches

The user chose a built-in catalog that prints every rule and supports selecting one rule. This provides complete output while allowing a reader to inspect one finding.

Printing every rule without a selector requires searching the complete output. Requiring a selector prevents browsing all examples. The user did not choose either approach.

The agent recommends embedding the catalog in the existing Go engine and exposing it through the shared Make target. Fetching examples from external documentation during command execution would require network access and would not provide built-in examples.

## Constraints and agent decisions

The agent selects plain text output with consistent indentation. The command prints examples without running lint tools or executing example code. After normal engine provisioning, the catalog does not require additional downloads or tool installation.

The catalog covers the shared configuration shipped with the engine. Discovering consumer-specific rules or interpreting local configuration overrides falls outside this change.

Use one catalog for the Make target and the engine command. Add the target to `make help` and expose it in this repository and consumer repositories.

## Verification

Run the built command and Make target to verify complete output, selection of one rule, and failure for an unknown identifier. Verify consumer access through the shared Make include.

Validate each example against the relevant production analyzer or tool. The bad example must trigger its stated rule. The good example must avoid that finding. Compare catalog coverage with the enabled rules from the configured tool versions and custom analyzer registration.

Run the repository's required build checks before committing implementation changes. Do not change lint enforcement, exclusions, or baselines to accommodate examples.
