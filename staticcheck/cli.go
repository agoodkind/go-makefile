package staticcheck

import (
	"os"

	"golang.org/x/tools/go/analysis/multichecker"
)

// Run executes the analyzers or formats argument lists.
//
// The Go analysis driver loads packages and reports analyzer diagnostics.
// The -format-arguments option formats the supplied source file paths.
//
// A command can delegate execution with:
//
//	func main() {
//		slog.Info("run static checks")
//		os.Exit(staticcheck.Run())
//	}
func Run() int {
	if len(os.Args) > 1 && os.Args[1] == "-format-arguments" {
		err := FormatArguments(os.Args[2:])
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")

			return 1
		}

		return 0
	}

	err := fixGeneratedArguments(os.Args[1:])
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")

		return 1
	}

	multichecker.Main(Analyzers()...)

	return 0
}
