# Test-habit analyzers

The `staticcheck-extra` binary includes four analyzers for `_test.go` files. They report tests that can pass while the behavior under test is broken. The analyzers are off by default.

When enabled, their findings pass through the same baseline gate as every other `staticcheck-extra` finding: a finding in the committed baseline passes, and a new finding fails. The baseline key ignores line and column, and a baselined finding that moves inside its file still passes.

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

`STATICCHECK_EXTRA_TEST_FLAGS` lists the enabled test-habit analyzers as flags, and the default `STATICCHECK_EXTRA_FLAGS` includes the list. The default is empty. Set the variable before `include bootstrap.mk`, or on the make command line, to enable analyzers:

```make
STATICCHECK_EXTRA_TEST_FLAGS := -testpackage -testassert -testdouble -testsourcefile
```

A consumer that assigns `STATICCHECK_EXTRA_FLAGS` itself adds the flags to that assignment. `STATICCHECK_EXTRA_EXCLUDE_PATHS` drops test-habit findings for matching paths, the same way it drops other findings.

The gate drops every `_test.go` finding by default through `STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS`. An enabled test-habit analyzer removes that default entry. The other analyzers skip test files themselves.
