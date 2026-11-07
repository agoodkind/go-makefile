# Source structure implementation plan

> Use executing-plans to implement each task. Track completion with checkboxes.

Goal: Enforce the five approved command, formatting, filename, and notice rules.

Architecture: Add commandentry, filenameprefix, and argumentlines analyzers to
staticcheck-extra. Use suggested fixes for argument layout and golangci-lint
for wsl_v5. Reuse applied notices for historical findings.

Tech stack: Go, go/ast, go/analysis, golangci-lint, and Make.

Spec: [Approved scope](../specs/2026-11-07-source-structure-design.md).

## Global constraints

Keep command implementation in importable packages outside cmd.
Keep short calls and simple declarations on one line.
Wrap declarations with inline callback parameters and put wrapped list elements on separate lines.
Use the exact wsl_v5 settings in the specification.
Require two distinct filename stems and three shared prefix characters.
Normalize ordinary Go test and platform suffixes before comparing stems.
Report required directory changes without automatically renaming files.
Use the existing applied-notice mechanism for historical findings.
The user requested implementation directly; continue without another approval.

## Review focus

Delegating main functions must also pass the existing boundary-log check.
Inline callback parameters must wrap declarations without wrapping simple declarations.
Nested suggested fixes must not overwrite each other.
Test and platform variants of one stem must not create a filename group.
A new member of a historical group must fail after notice application.

## Task 1: Check command entry points

Create [entry.go](../../../staticcheck/entry.go) for CommandEntryAnalyzer
and the shared predicate that recognizes an imported command delegation.
Update [boundary.go](../../../staticcheck/boundary.go) to accept that shape.
Use the public analyzer interface with real fixture packages for regression tests.

- [x] Implement CommandEntryAnalyzer with the commandentry flag.
- [x] Verify a delegating command passes and local implementation fails.

## Task 2: Check repeated filename prefixes

Create [prefix.go](../../../staticcheck/prefix.go) for FilenamePrefixAnalyzer.
Compare distinct normalized sibling stems. Prefer separator boundaries and
otherwise detect the shared character prefix. Report each affected file.

- [x] Implement FilenamePrefixAnalyzer with the filenameprefix flag.
- [x] Verify both prefix spellings, separate directories, and Go suffixes.

## Task 3: Format arguments and callback declarations

Create [arguments.go](../../../staticcheck/arguments.go) for
ArgumentLinesAnalyzer. Include inline callback declarations. Provide suggested fixes that preserve comments and
variadic arguments. Register all three analyzers in
[analyzers.go](../../../staticcheck/analyzers.go).

- [x] Implement ArgumentLinesAnalyzer with the argumentlines flag.
- [x] Verify short calls, wrapped calls, nested calls, and fixes.

## Task 4: Integrate formatting and historical baselines

Keep formatting implementation in [internal/format](../../../internal/format).
Delegate from [lint_scoped.go](../../../cmd/go-mk/lint_scoped.go) so make fmt
applies argumentlines and wsl_v5 fixes before the existing formatters. Update
[go.mk](../../../go.mk) to enable the analyzers and
[golangci.yml](../../../golangci.yml) with the exact approved settings.
Add narrowly scoped effective-date notices to [notices.txt](../../../notices.txt).

- [x] Enable the checks and formatter fixes.
- [x] Verify make fmt through a real consumer and verify a second run is stable.
- [x] Verify notice baselines accept historical findings and reject later ones.
- [x] Run the focused tests and the repository's make build verification.
- [x] Review the complete change against the approved scope and commit it.

The repository rejects make check and directs callers to make build.
