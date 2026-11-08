// Package diagnostics normalizes captured findings and identifies source occurrences.
package diagnostics

import (
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

// GolangciExcludePattern preserves caller exclusions and admits whitespace findings in tests.
func GolangciExcludePattern() string {
	defaults := os.Getenv("GOLANGCI_LINT_DEFAULT_EXCLUDE_PATHS")
	if defaults == "" {
		defaults = `_test\.go:`
	}

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

// A line without this suffix is shorter than the suffix or differs at one character.
func exceptSuffix(suffix string) string {
	options := []string{".{0," + strconv.Itoa(len(suffix)-1) + "}"}
	for index, character := range suffix {
		rest := regexp.QuoteMeta(suffix[index+1:])
		options = append(options, ".*[^"+regexp.QuoteMeta(string(character))+"]"+rest)
	}

	return "(?:" + strings.Join(options, "|") + ")$"
}

// Extract reads captured findings and adds source identities to WSL diagnostics.
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

	if _, err := os.Stat(target); err != nil {
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

// CaptureNotice rejects captures that discard findings outside the current tree.
func CaptureNotice[Mode ~int](
	rawPath string,
	findingsPath string,
	pattern *regexp.Regexp,
	capture func(string, string, Mode) error,
	mode Mode,
	rootPath func() string,
) error {
	if err := capture(rawPath, findingsPath, mode); err != nil {
		return err
	}

	_, dropped, err := Extract(rawPath, pattern.String(), "", rootPath)
	if err != nil {
		return err
	}

	if dropped > 0 {
		return fmt.Errorf("notice capture discarded %d findings outside the current tree; rerun with a fresh GOLANGCI_LINT_CACHE directory", dropped)
	}

	return nil
}
