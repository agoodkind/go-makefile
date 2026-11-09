package notice

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"log/slog"
	"regexp"
	"strconv"

	"goodkind.io/go-makefile/internal/baseline"
)

var duplicatePattern = regexp.MustCompile(
	"^(.+\\.go):[0-9]+:[0-9]+: ([0-9]+)-([0-9]+) lines are duplicate of `([^`]+\\.go):([0-9]+)-([0-9]+)` \\(dupl\\)$",
)

// SourceKey removes dupl range coordinates for historical occurrence matching.
//
// Both paths remain part of the identity. Ordinary baseline keys and rendered
// diagnostics retain their original coordinates.
//
// For example:
//
//	SourceKey("sample.go:3:1: 3-8 lines are duplicate of `peer.go:10-15` (dupl)")
func SourceKey(finding string) string {
	match := duplicatePattern.FindStringSubmatch(finding)
	if match == nil {
		return baseline.Key(finding)
	}

	return baseline.Key(
		match[1] + ":1:1: <source> lines are duplicate of `" + match[4] + ":<source>` (dupl)",
	)
}

func duplicateIdentity(
	sources map[string]Source,
	finding string,
	root string,
	match []string,
) (string, error) {
	first, err := blockFingerprint(sources, root, match[1], match[2], match[3])
	if err != nil {
		return "", err
	}

	second, err := blockFingerprint(sources, root, match[4], match[5], match[6])
	if err != nil {
		return "", err
	}

	digest := sha256.Sum256([]byte(first + "\x00" + second))

	return fmt.Sprintf("%s\x00%x", SourceKey(finding), digest), nil
}

func blockFingerprint(sources map[string]Source, root, file, first, last string) (string, error) {
	start, err := strconv.Atoi(first)
	if err != nil {
		slog.Error(
			"notice parse duplicate start line",
			slog.String("file", file),
			slog.Any("error", err),
		)

		return "", fmt.Errorf("notice duplicate start line: %w", err)
	}

	end, err := strconv.Atoi(last)
	if err != nil {
		slog.Error(
			"notice parse duplicate end line",
			slog.String("file", file),
			slog.Any("error", err),
		)

		return "", fmt.Errorf("notice duplicate end line: %w", err)
	}

	parsed, err := loadSource(sources, root, file)
	if err != nil {
		return "", err
	}

	if start < 1 || end < start || end > parsed.files.File(parsed.file.Pos()).LineCount() {
		return "", &duplicateRangeError{file: file, start: start, end: end, missing: false}
	}

	var nodes []ast.Node

	for _, declaration := range parsed.file.Decls {
		if parsed.files.Position(declaration.End()).Line >= start &&
			parsed.files.Position(declaration.Pos()).Line <= end {
			nodes = append(nodes, declaration)
		}
	}

	if len(nodes) == 0 {
		return "", &duplicateRangeError{file: file, start: start, end: end, missing: true}
	}

	return parsed.fingerprint(nodes)
}
