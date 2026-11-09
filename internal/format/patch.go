package format

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	errFormatterPatch = errors.New("invalid configured formatter patch")
	hunkPattern       = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: .*)?$`)
)

const patchHeaderLines = 3

type hunkRange struct {
	start int
	count int
}

func applyFormatterPatch(output string, source map[string][]byte) (map[string][]byte, error) {
	formatted := maps.Clone(source)
	if output == "" {
		return formatted, nil
	}

	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	seen := make(map[string]bool)

	for cursor := 0; cursor < len(lines); {
		path, next, err := patchHeader(lines, cursor)
		if err != nil {
			return nil, err
		}

		original, selected := source[path]
		if !selected || seen[path] {
			return nil, failure(
				"decode formatter patch",
				fmt.Errorf("%w: unexpected file %s", errFormatterPatch, path),
			)
		}

		seen[path] = true
		result, after, err := patchFile(lines, next, sourceLines(original))
		if err != nil {
			return nil, failure("apply formatter patch", fmt.Errorf("%w: %s", err, path))
		}

		formatted[path] = []byte(strings.Join(result, ""))
		cursor = after
	}

	return formatted, nil
}

func patchHeader(lines []string, cursor int) (string, int, error) {
	if cursor+2 >= len(lines) || !strings.HasPrefix(lines[cursor+1], "--- ") ||
		!strings.HasPrefix(lines[cursor+2], "+++ ") {
		return "", cursor, failure(
			"decode formatter header",
			fmt.Errorf("%w: unexpected output %q", errFormatterPatch, lines[cursor]),
		)
	}

	oldPath := strings.TrimPrefix(lines[cursor+1], "--- ")
	newPath := strings.TrimPrefix(lines[cursor+2], "+++ ")
	if oldPath != newPath+".orig" || lines[cursor] != "diff "+oldPath+" "+newPath {
		return "", cursor, failure(
			"decode formatter header",
			fmt.Errorf("%w: inconsistent file header", errFormatterPatch),
		)
	}

	absolute, err := filepath.Abs(newPath)
	if err != nil {
		return "", cursor, failure("resolve formatter patch source", err)
	}

	return absolute, cursor + patchHeaderLines, nil
}

func patchFile(lines []string, cursor int, source []string) ([]string, int, error) {
	var result []string
	previous := 0
	hunks := 0

	for cursor < len(lines) && strings.HasPrefix(lines[cursor], "@@ ") {
		oldRange, newRange, err := parseHunk(lines[cursor])
		if err != nil {
			return nil, cursor, err
		}

		if oldRange.start < previous || oldRange.start > len(source) {
			return nil, cursor, failure(
				"apply formatter file",
				fmt.Errorf("%w: hunk exceeds source", errFormatterPatch),
			)
		}

		result = append(result, source[previous:oldRange.start]...)
		if len(result) != newRange.start {
			return nil, cursor, failure(
				"apply formatter file",
				fmt.Errorf("%w: inconsistent hunk position", errFormatterPatch),
			)
		}

		changed, next, err := patchHunk(lines, cursor+1, source, oldRange, newRange)
		if err != nil {
			return nil, cursor, err
		}

		result = append(result, changed...)
		previous = oldRange.start + oldRange.count
		cursor = next
		hunks++
	}

	if hunks == 0 {
		return nil, cursor, failure(
			"apply formatter file",
			fmt.Errorf("%w: missing hunk", errFormatterPatch),
		)
	}

	return append(result, source[previous:]...), cursor, nil
}

func parseHunk(header string) (hunkRange, hunkRange, error) {
	parts := hunkPattern.FindStringSubmatch(header)
	if parts == nil {
		return hunkRange{}, hunkRange{}, failure(
			"decode formatter hunk",
			fmt.Errorf("%w: invalid hunk header", errFormatterPatch),
		)
	}

	oldRange, err := parseRange(parts[1], parts[2])
	if err != nil {
		return hunkRange{}, hunkRange{}, err
	}

	newRange, err := parseRange(parts[3], parts[4])

	return oldRange, newRange, err
}

func parseRange(startText, countText string) (hunkRange, error) {
	start, err := strconv.Atoi(startText)
	if err != nil {
		return hunkRange{}, failure(
			"decode formatter range",
			fmt.Errorf("%w: %w", errFormatterPatch, err),
		)
	}

	count := 1
	if countText != "" {
		count, err = strconv.Atoi(countText)
		if err != nil {
			return hunkRange{}, failure(
				"decode formatter range",
				fmt.Errorf("%w: %w", errFormatterPatch, err),
			)
		}
	}

	if count > 0 {
		start--
	}

	if start < 0 {
		return hunkRange{}, failure(
			"decode formatter range",
			fmt.Errorf("%w: invalid source position", errFormatterPatch),
		)
	}

	return hunkRange{start: start, count: count}, nil
}

func patchHunk(
	lines []string,
	cursor int,
	source []string,
	oldRange hunkRange,
	newRange hunkRange,
) ([]string, int, error) {
	var result []string

	oldCount, newCount := 0, 0
	for oldCount < oldRange.count || newCount < newRange.count {
		if cursor >= len(lines) || lines[cursor] == "" {
			return nil, cursor, failure(
				"apply formatter hunk",
				fmt.Errorf("%w: incomplete hunk", errFormatterPatch),
			)
		}

		line := lines[cursor]
		text := line[1:] + "\n"
		cursor++
		if cursor < len(lines) && lines[cursor] == `\ No newline at end of file` {
			text = strings.TrimSuffix(text, "\n")
			cursor++
		}

		if line[0] == ' ' || line[0] == '-' {
			index := oldRange.start + oldCount
			if index >= len(source) || source[index] != text {
				return nil, cursor, failure(
					"apply formatter hunk",
					fmt.Errorf("%w: source context differs", errFormatterPatch),
				)
			}

			oldCount++
		}

		if line[0] == ' ' || line[0] == '+' {
			result = append(result, text)
			newCount++
		} else if line[0] != '-' {
			return nil, cursor, failure(
				"apply formatter hunk",
				fmt.Errorf("%w: invalid hunk line", errFormatterPatch),
			)
		}

		if oldCount > oldRange.count || newCount > newRange.count {
			return nil, cursor, failure(
				"apply formatter hunk",
				fmt.Errorf("%w: inconsistent hunk counts", errFormatterPatch),
			)
		}
	}

	return result, cursor, nil
}

func sourceLines(source []byte) []string {
	if len(source) == 0 {
		return nil
	}

	lines := strings.SplitAfter(string(source), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return lines
}
