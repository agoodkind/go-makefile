// Package diagnostics normalizes findings and identifies their source.
package diagnostics

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
)

var errDroppedFindings = errors.New("notice capture discarded")

// GolangciExcludePattern builds the path filter for golangci-lint findings.
//
// A legacy default test exclusion admits wsl_v5 findings in test files.
// An explicit exclusion applies to every linter, including wsl_v5.
//
// For example, this setting excludes all test findings:
//
//	GOLANGCI_LINT_EXCLUDE_PATHS=_test\.go:
func GolangciExcludePattern() string {
	defaults := os.Getenv("GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS")

	var patterns []string

	for pattern := range strings.SplitSeq(defaults, ",") {
		if pattern == "" {
			continue
		}

		if pattern == `_test\.go:` {
			pattern += exceptSuffix(whitespaceTag)
		}

		patterns = append(patterns, pattern)
	}

	if extra := lint.ExcludePattern("", os.Getenv("GOLANGCI_LINT_EXCLUDE_PATHS")); extra != "" {
		patterns = append(patterns, extra)
	}

	return strings.Join(patterns, "|")
}

// A different ending is shorter than the suffix or differs at one character.
func exceptSuffix(suffix string) string {
	options := make([]string, 1, len(suffix)+1)
	options[0] = ".{0," + strconv.Itoa(len(suffix)-1) + "}"

	for index, character := range suffix {
		rest := regexp.QuoteMeta(suffix[index+1:])
		options = append(options, ".*[^"+regexp.QuoteMeta(string(character))+"]"+rest)
	}

	return "(?:" + strings.Join(options, "|") + ")$"
}

// Extract reads lint findings and returns the number outside the worktree.
//
// It filters paths and identifies each whitespace finding by its source
// statement. The identity survives a source line shift.
//
// A whitespace finding includes the identity before the linter name:
//
//	widget.go:7:2: <message> [source:<fingerprint>] (wsl_v5)
func Extract(
	rawPath string,
	matchPattern string,
	excludePattern string,
	rootPath func() string,
) ([]string, int, error) {
	slog.Info("lint extract findings", slog.String("raw", rawPath))
	lines, err := readLines(rawPath)
	if err != nil {
		return nil, 0, err
	}

	pattern, err := regexp.Compile(matchPattern)
	if err != nil {
		return nil, 0, failure("compile finding pattern", err)
	}

	var exclusion *regexp.Regexp
	if excludePattern != "" {
		exclusion, err = regexp.Compile(excludePattern)
		if err != nil {
			return nil, 0, failure("compile finding exclusion", err)
		}
	}

	root := rootPath()
	kept := make([]string, 0, len(lines))
	droppedOutOfTree := 0
	sources := make(map[string]*source)

	for _, line := range lines {
		if !pattern.MatchString(line) {
			continue
		}

		normalized := findings.NormalizePath(line, root, root)
		if exclusion != nil && exclusion.MatchString(normalized) {
			continue
		}

		if path, ok := findings.FindingPath(normalized); ok && !resolvesInTree(path, root) {
			droppedOutOfTree++

			continue
		}

		normalized, err := whitespaceIdentity(normalized, root, sources)
		if err != nil {
			return nil, 0, err
		}

		kept = append(kept, normalized)
	}

	slices.Sort(kept)

	return slices.Compact(kept), droppedOutOfTree, nil
}

func resolvesInTree(path, root string) bool {
	if path == "" {
		return true
	}

	rootClean := filepath.Clean(root)
	var target string
	if filepath.IsAbs(path) {
		target = filepath.Clean(path)
	} else {
		target = filepath.Clean(filepath.Join(rootClean, path))
	}

	if target != rootClean && !strings.HasPrefix(target, rootClean+string(filepath.Separator)) {
		return false
	}

	_, err := os.Stat(target)
	if err != nil {
		return !os.IsNotExist(err)
	}

	return true
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, failure("read captured findings", err)
	}

	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return []string{}, nil
	}

	return strings.Split(text, "\n"), nil
}

func failure(message string, err error) error {
	wrapped := fmt.Errorf("%s: %w", message, err)
	slog.Error(message, slog.Any("err", wrapped))

	return wrapped
}

// CaptureNotice rejects a notice capture with findings outside the worktree.
//
// It runs the capture and checks the raw findings before a notice can
// record an incomplete baseline.
//
// A capture with two out-of-tree paths reports:
//
//	notice capture discarded 2 findings outside the current tree
func CaptureNotice[Mode ~int](
	rawPath string,
	findingsPath string,
	pattern *regexp.Regexp,
	capture func(string, string, Mode) error,
	mode Mode,
	rootPath func() string,
) error {
	err := capture(rawPath, findingsPath, mode)
	if err != nil {
		return err
	}

	_, dropped, err := Extract(rawPath, pattern.String(), "", rootPath)
	if err != nil {
		return err
	}

	if dropped > 0 {
		err := fmt.Errorf(
			"%w %d findings outside the current tree; rerun with a fresh GOLANGCI_LINT_CACHE directory",
			errDroppedFindings,
			dropped,
		)

		return failure("validate notice capture", err)
	}

	return nil
}
