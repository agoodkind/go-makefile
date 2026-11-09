// Package extract selects and normalizes lint diagnostics.
package extract

import (
	"log/slog"
	"regexp"

	"goodkind.io/go-makefile/internal/findings"
)

// Findings reads and filters diagnostics from a tool output file.
//
// It counts diagnostics for paths that do not resolve in the worktree.
//
// For example, a caller can provide its file reader and path checks:
//
//	Findings(path, pattern, exclude, read, root, resolves, unique)
func Findings(
	rawPath string,
	matchPattern string,
	excludePattern string,
	read func(string) ([]string, error),
	root func() string,
	resolves func(string, string) bool,
	unique func([]string) []string,
) ([]string, int, error) {
	slog.Info("lint extract findings", slog.String("raw", rawPath))
	lines, err := read(rawPath)
	if err != nil {
		return nil, 0, err
	}

	pattern, err := regexp.Compile(matchPattern)
	if err != nil {
		return nil, 0, err
	}

	worktree := root()
	kept := make([]string, 0, len(lines))
	dropped := 0

	for _, line := range findings.JoinDiagnosticLines(lines) {
		if !pattern.MatchString(line) {
			continue
		}

		normalized := findings.NormalizePath(line, worktree, worktree)
		if excludePattern != "" {
			exclude, compileErr := regexp.Compile(excludePattern)
			if compileErr != nil {
				return nil, 0, compileErr
			}

			if exclude.MatchString(normalized) {
				continue
			}
		}

		if path, ok := findings.FindingPath(normalized); ok && !resolves(path, worktree) {
			dropped++

			continue
		}

		kept = append(kept, normalized)
	}

	return unique(kept), dropped, nil
}
