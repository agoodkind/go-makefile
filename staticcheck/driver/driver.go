// Package driver dispatches analyzer and source-formatting commands.
package driver

import (
	"log/slog"
	"os"

	"golang.org/x/tools/go/analysis/multichecker"

	"goodkind.io/go-makefile/staticcheck"
	"goodkind.io/go-makefile/staticcheck/multilineargs"
)

// Run dispatches the source formatter or the registered analyzers.
//
// The formatting mode reads the selected source paths from the command line.
//
// Run formatting with:
//
//	staticcheck-extra --format-multilineargs example.go
func Run() int {
	if len(os.Args) > 1 && os.Args[1] == "--format-multilineargs" {
		if err := multilineargs.FormatPaths(os.Args[2:]); err != nil {
			slog.Error("format multiline arguments", slog.Any("err", err))

			return 1
		}

		return 0
	}

	multichecker.Main(staticcheck.Analyzers()...)

	return 0
}
