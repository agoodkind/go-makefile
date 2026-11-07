# Enforce Go command structure and formatting

Require thin command entry points, consistent argument layout, and directories
for groups of Go files that repeat a filename prefix. Introduce the checks
through applied notices so eligible historical findings can remain in baselines.

## Goals

- Keep implementation outside command entry points.
- Keep short calls on one line and format wrapped argument lists consistently.
- Apply the requested whitespace rules.
- Detect repeated filename prefixes with and without separators.
- Reject later violations after a consumer applies a notice.

## Approaches

Use the existing staticcheck-extra gate for the command, argument-layout, and
filename checks. Enable wsl_v5 in the shared golangci configuration. Applied
notices already support both gates. This approach reuses their baseline and
effective-date handling.

A separate checker would need additional gate, baseline, and notice handling.
A vocabulary of approved words would not establish whether sibling filenames
repeat a prefix. Neither alternative fits the requested structural check and
existing notice integration.

## Require thin command entry points

For production Go files in command packages under a module's cmd directory,
permit imports and main only. main calls an imported command entry point and
may pass its integer result to os.Exit. Permit direct forwarding of process
arguments and standard input, output, and error streams.

Reject helper functions, methods, local types, package variables, constants,
flag parsing, dependency construction, and control flow in these command
packages. Implement those operations in importable packages outside cmd.
Tests can invoke the built command through its public interface.

Make the boundary-log analyzer accept this delegating main shape. Command
implementation remains subject to its ordinary boundary-log checks. Do not
require a logging statement solely to satisfy the entry-point check.

These syntax details are agent-proposed decisions for design approval.

## Format arguments and whitespace

Keep current decisions about whether a call wraps. This change does not add a
line-length limit or force a short call such as run(ctx, args) to wrap.

Treat an argument list as wrapped when a newline separates the opening
parenthesis from the first argument, or separates adjacent arguments. For a
wrapped list with several arguments, put each argument on its own line and put
the closing parenthesis on its own line. Preserve comments and variadic calls.
Newlines only inside a function literal or another argument do not make the
outer argument list wrapped. This distinction is an agent-proposed decision.

Report argument-layout violations through staticcheck-extra so applied notices
can baseline historical findings. Make make fmt apply the argument fixes and
wsl_v5 fixes, then run the existing formatters. Preserve formatter stability:
a second make fmt must not change the output of the first.

Keep the existing formatter-diff gate separate from the new baselined argument
check. A raw diff of the new formatting would fail on historical findings that
the notice accepts.

Enable wsl_v5 with these settings under linters.settings:

```yaml
wsl_v5:
  default: none
  enable:
    - after-block
    - return
    - err
    - for
    - range
  allow-first-in-block: false
  allow-whole-block: false
  branch-max-lines: 2
  case-max-lines: 0
  cuddle-max-statements: 1
```

Keep the legacy wsl linter disabled. Preserve the current golangci test-file
exclusion. Apply the argument-layout check to handwritten production and test
Go files.

## Require directories for repeated filename prefixes

Compare sibling Go filenames within each analyzed package directory. Remove
the extension and standard Go test, operating-system, and architecture suffixes
before comparing names. Count each resulting stem once. A production file and
its test or platform variants do not create a filename family by themselves.
Use the existing generated-file recognition to exclude generated source.

Detect groups with at least two distinct stems and a shared prefix of at least
three characters. These thresholds are agent-proposed decisions. Match
bar_abc with bar_xyz and barabc with barxyz. Do not consult a word dictionary.

Prefer a shared prefix that ends before an underscore or hyphen. Otherwise use
an existing sibling stem as the prefix when at least two other stems extend
it, or use the shared prefix where the names diverge. Select the outermost
qualifying group before considering more specific groups. Require at least
two distinct nonempty suffixes after removing the group prefix and separators.

For example, require bar/abc.go and bar/xyz.go for either spelling of that
family. Report the group prefix and affected filename in each diagnostic.
Detecting a group must not rename files or rewrite package declarations.

The check enforces repeated prefixes across sibling Go files. It does not
attempt to decide whether every individual filename is an English noun or
verb. Keep diagnostics specific enough that a new member of a historical
group cannot match another member's baseline entry.

## Introduce checks through applied notices

Create separate notices for each custom check and for wsl_v5. Give each notice
an effective date and a scope limited to its check. Determine the actual dates
when preparing the implementation; do not backdate the notices.

Reuse the current historical comparison. Baseline only findings that also
existed before the notice's effective date. Record a notice as applied only
after its required work succeeds. Preserve the current behavior in CI, which
reports unapplied notices and does not write their baseline files.

Do not refactor every existing consumer or grant permanent exceptions to
existing filename families. Historical baselines provide the requested
transition. Automatic formatting may update existing formatting when a
consumer explicitly runs make fmt.

## Verify public behavior

Use real temporary consumer repositories and the built commands to verify:

- A delegating command passes, and implementation inside cmd fails.
- A short call passes, a wrapped list with shared argument lines fails, and
  make fmt corrects it without changing comments or command behavior.
- The supplied wsl_v5 settings reject and fix representative spacing errors.
- Both filename spellings fail, while a shared directory passes.
- Test and platform variants alone do not create a filename family.
- Applied notices accept eligible historical findings and reject later files,
  new group members, and later implementation or formatting violations.
- CI does not apply notices, and failed historical analysis does not record
  a notice as applied.

Run the focused public-boundary tests, make check, and the relevant nested
staticcheck module checks before claiming implementation completion.

## Scope and approval

This project changes shared enforcement and formatting. It does not deploy
consumer refactors or merge changes. Review the proposed thresholds, command
shape, and wrapping behavior before implementation.
