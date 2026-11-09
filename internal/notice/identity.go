// Package notice compares current findings with historical source.
package notice

import (
	"strings"

	"goodkind.io/go-makefile/internal/baseline"
)

// Identity selects the source identity used by a historical notice.
//
// Command-entry diagnostics already include a complete declaration digest.
// Other diagnostics retain source-occurrence matching.
//
// A declaration diagnostic includes its digest and occurrence:
//
//	commandentry: delegate implementation [id=digest:1]
func Identity(
	finding string,
	root string,
	gate string,
	rule string,
	source func(string, string) (string, error),
) (string, error) {
	if gate == "golangci" && rule == "file-length-limit" {
		return baseline.Key(finding), nil
	}

	if gate == "staticcheck-extra" && strings.Contains(finding, ": commandentry:") {
		return baseline.Key(finding), nil
	}

	return source(finding, root)
}
