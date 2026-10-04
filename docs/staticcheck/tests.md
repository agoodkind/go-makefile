# Test-habit analyzers

The `staticcheck-extra` gate runs a second analyzer pass over `_test.go` files. The pass reports tests that can pass while the behavior under test is broken.

## New test code is blocked

A finding on a line added relative to the base commit fails the gate. A finding on an older line is advisory: the gate prints it and still passes. A repository with existing findings needs no baseline and no cleanup. The findings never enter a baseline file.

The added lines are the lines that `git diff` reports for `_test.go` files between the base commit and the working tree, plus every line of each untracked `_test.go` file. Each analyzer reports at one line: the package clause, the test function name, the type declaration, the import, or the file read. A new test file, a new test function, a new test double type, and a new file read are blocked. An edit inside an older test does not block.

The base commit is the merge-base of `HEAD` and the default branch on `origin`. On the default branch the base is `HEAD`, and only uncommitted test code is blocked.

A GitHub Actions job checks out one commit with no history. In that shallow checkout the gate fetches one commit as the base: the tip of the default branch for a branch or pull request build, and the commit before the push for a push to the default branch. A branch behind the default branch is compared with the current tip. Rebase the branch when the gate blocks a test that the default branch already removed.

Git reports a renamed file with unchanged content as a rename with no added line. A rename of an older test file does not block.

When no base resolves, for example outside a git repository, every finding is advisory and the report states that.

`make check` shows the gate as `FAILED` for a finding in new test code and as `ADVISORY` when only older findings exist. The advisory report shows the first 20 findings and the count of the rest. The full list is in `.make/staticcheck-extra-advisory.out`.

## Analyzers

| Analyzer | Reports | Required change |
| --- | --- | --- |
| `testpackage` | A `_test.go` file that declares the package under test. | Declare the external package `<name>_test` and call only exported identifiers. For package `main`, run the built command. |
| `testassert` | A `TestXxx` function or a `t.Run` function literal with no statement able to fail the test. | Assert an outcome with `t.Error`, `t.Fatal`, or a helper with a `testing.TB` parameter. |
| `testdouble` | A type declared in a `_test.go` file and converted to an interface that a non-test file of the same module declares. Also an import of a mock library. | Run the production implementation with real dependencies. |
| `testsourcefile` | A test that reads a file by constant path with `os.ReadFile`, `os.Open`, or `os.OpenFile`, or embeds a file with `//go:embed`. | Run the code that uses the file and assert on the outcome, or move the test input under `testdata`. |

## Exemptions

- `testpackage` skips a file named `export_test.go`.
- `testassert` skips a test body made only of `t.Skip`, `t.Skipf`, or `t.SkipNow` calls.
- `testdouble` skips a type with the directive `//testdouble:external <reason>` in the doc comment of the type. The reason has at least three words and states which service outside the host the type fakes. The directive does not apply to a mock library import.
- `testsourcefile` skips a path with a `testdata` element, a path with a non-constant part such as `t.TempDir()`, and a constant path under `/dev/`, `/proc/`, `/sys/`, or `/etc/`.

The mock libraries are `github.com/golang/mock`, `go.uber.org/mock`, `github.com/stretchr/testify/mock`, `github.com/vektra/mockery`, and `github.com/maxbrunsfeld/counterfeiter`.

## Known limits

- `testassert` counts any function with a `testing.TB` argument as able to fail the test, including a helper that never asserts.
- `testassert` reports a test that fails only through a panic or through a helper that captures `t` in a closure.
- `testdouble` does not detect a conversion through a channel send, a generic type argument, or a multi-value `return f()`.
- `testsourcefile` does not report a path built from a variable or a function result.

## Configuration

`STATICCHECK_EXTRA_ADVISORY_FLAGS` lists the test-habit analyzers as flags. The default enables all four. Set the variable before `include bootstrap.mk` to run a subset:

```make
STATICCHECK_EXTRA_ADVISORY_FLAGS := -testpackage -testassert
```

An empty value turns the pass off. `STATICCHECK_EXTRA_EXCLUDE_PATHS` drops test-habit findings for matching paths, the same way it drops gated findings.

| Variable | Default | Effect |
| --- | --- | --- |
| `STATICCHECK_EXTRA_TEST_BLOCK` | `new` | `off` keeps every test-habit finding advisory. |
| `STATICCHECK_EXTRA_TEST_BASE` | empty | A commit to use as the base in place of the merge-base with the default branch. |

The gate skips the test-habit pass when the resolved `staticcheck-extra` binary predates a listed analyzer.
