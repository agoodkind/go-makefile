# Test-habit analyzers

The `staticcheck-extra` binary includes five analyzers for `_test.go` files. They report tests that can pass while the behavior under test is broken. The analyzers are off by default.

When enabled, their findings pass through the same baseline gate as every other `staticcheck-extra` finding: a finding in the committed baseline passes, and a new finding fails. The baseline key ignores line and column, and a baselined finding that moves inside its file still passes.

## Analyzers

| Analyzer | Reports | Required change |
| --- | --- | --- |
| `testpackage` | A test function in the package under test that calls an unexported function of that package. The finding states the first such function. | Call the exported entry point that uses the function. For package `main`, run the built command. |
| `testpackage` | A `_test.go` file that declares the package under test and has no test with an unexported call. | Declare the external package `<name>_test`. |
| `testassert` | A `TestXxx` function or a `t.Run` function literal with no statement able to fail the test. | Assert an outcome with `t.Error`, `t.Fatal`, or a helper with a `testing.TB` parameter. |
| `testassert` | A test with failing statements guarded only by an error nil comparison. | Assert on the returned value, on the state that the call changed, or on the kind of the error. |
| `testdouble` | A type declared in a `_test.go` file and converted to an interface that a non-test file of the same module declares. Also an import of a mock library. | Run the production implementation with real dependencies. |
| `testseam` | A test that assigns a package-level variable of function or interface type declared in a non-test file of the same module. | Run the production value. |
| `testseam` | A test that sets a function field of a production struct to a value other than the single value that production code sets. | Run the production function. |
| `testsourcefile` | A test that reads a file by constant path with `os.ReadFile`, `os.Open`, or `os.OpenFile`, reads through a `..` path element, embeds a file with `//go:embed`, or calls `go/parser`. | Run the code that uses the file and assert on the outcome, or move the test input under `testdata`. |

A file gets one `testpackage` finding per test with an unexported call, or one finding at the package clause, and never both.

## Exemptions

- `testpackage` skips a file named `export_test.go`.
- `testassert` skips a test body made only of `t.Skip`, `t.Skipf`, or `t.SkipNow` calls.
- The error nil finding of `testassert` skips three cases: a test that requires a nil error and discards no other result of the call; a test that calls `os.Stat` or `os.Lstat`; a test that stores a `testing.TB` value in a composite literal.
- `testdouble` skips a type with the directive `//testdouble:external <reason>` in the doc comment of the type. The reason has at least three words and states which service outside the host the type fakes. The directive does not apply to a mock library import.
- `testdouble` skips a struct type with a field of the interface it implements. Such a type delegates to a real implementation.
- `testseam` skips a replacement with the directive `//testseam:external <reason>` on the same line or on the line above. The reason has at least three words.
- `testseam` skips an assignment that writes a saved copy back, as in `old := seam` followed by `seam = old`.
- `testseam` skips a function field with no production value in its declaring package, and a function field with several distinct production values. The second case is a callback.
- `testsourcefile` skips a read when the same function passes the content to a function of the module declared in a non-test file, or writes it to another file with `os.WriteFile`. The content includes each local variable assigned from it. Such a file is test input.
- `testsourcefile` skips a path with a `testdata` element, a path with a non-constant part and no `..` element, and a constant path under `/dev/`, `/proc/`, `/sys/`, or `/etc/`.

The mock libraries are `github.com/golang/mock`, `go.uber.org/mock`, `github.com/stretchr/testify/mock`, `github.com/vektra/mockery`, and `github.com/maxbrunsfeld/counterfeiter`.

## Known limits

- `testassert` counts any function with a `testing.TB` argument as able to fail the test, including a helper that never asserts.
- `testassert` reports a test that fails only through a panic or through a helper that captures `t` in a closure.
- `testdouble` does not detect a conversion through a channel send, a generic type argument, or a multi-value `return f()`.
- `testseam` reports a clock or sleep replacement. It has no exemption by function type.
- `testseam` does not report a function passed as a call argument.
- `testsourcefile` does not report a path built from a helper function or a variable with no `..` element.
- `testsourcefile` reports a shared input file outside `testdata` when a different function than the reader passes the content to production code.
- No analyzer reports a pinned error or log wording, a restated implementation, or a test that passes with the protected behavior broken. `make mutation` measures the last case.

## Configuration

`STATICCHECK_EXTRA_TEST_FLAGS` lists the enabled test-habit analyzers as flags. The gate runs `STATICCHECK_EXTRA_FLAGS` followed by this list. The default is empty. Set the variable before `include bootstrap.mk`, or on the make command line, to enable analyzers:

```make
STATICCHECK_EXTRA_TEST_FLAGS := -testpackage -testassert -testdouble -testseam -testsourcefile
```

A consumer that assigns `STATICCHECK_EXTRA_FLAGS` itself needs no change to that assignment. `STATICCHECK_EXTRA_EXCLUDE_PATHS` drops test-habit findings for matching paths, the same way it drops other findings.

The gate drops every `_test.go` finding by default through `STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS`. An enabled test-habit analyzer removes that default entry. The other analyzers skip test files themselves.
