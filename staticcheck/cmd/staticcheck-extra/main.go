// Command staticcheck-extra runs the custom AST analyzer set that ships
// with go-makefile's `staticcheck-extra` Make target. Invoked by go.mk
// with the analyzer flags (-slog_error_without_err, -hot_loop_info_log,
// etc.) and a package list.
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/agoodkind/go-makefile/staticcheck"
)

func main() {
	multichecker.Main(staticcheck.Analyzers()...)
}
