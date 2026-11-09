package execution

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	recordFieldCount  = 3
	summaryFieldIndex = 2
)

func readLines(path string) ([]string, error) {
	slog.Info("notice read file", slog.String("path", path))
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fileFailure("read notice file", err)
	}

	text := strings.TrimSuffix(string(contents), "\n")
	if text == "" {
		return nil, nil
	}

	return strings.Split(text, "\n"), nil
}

func readRecords(path string) ([]record, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}

	var records []record

	for _, line := range lines {
		fields := strings.SplitN(line, "\t", recordFieldCount)
		id := fields[0]
		if id == "" || strings.HasPrefix(id, "#") {
			continue
		}

		current := record{id: id, directive: "", summary: ""}
		if len(fields) > 1 {
			current.directive = fields[1]
		}

		if len(fields) > summaryFieldIndex {
			current.summary = fields[summaryFieldIndex]
		}

		records = append(records, current)
	}

	return records, nil
}

func loadApplied(path string) (map[string]bool, error) {
	applied := make(map[string]bool)
	lines, err := readLines(path)
	if err != nil {
		return applied, err
	}

	for _, line := range lines {
		if value := strings.TrimSpace(line); value != "" {
			applied[value] = true
		}
	}

	return applied, nil
}

func loadSeen(path string) (int, error) {
	lines, err := readLines(path)
	if err != nil {
		return 0, err
	}

	if len(lines) == 0 {
		return 0, nil
	}

	value, _ := noticeNumber(strings.TrimSpace(lines[0]))

	return value, nil
}

func writeSeen(path string, value int) error {
	slog.Info("notice write seen", slog.String("path", path))
	if err := os.WriteFile(path, []byte(strconv.Itoa(value)+"\n"), 0o600); err != nil {
		return fileFailure("write seen notice", err)
	}

	return nil
}

func appendApplied(path, noticeID string) error {
	slog.Info("notice append applied", slog.String("path", path), slog.String("id", noticeID))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fileFailure("create applied notice directory", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fileFailure("open applied notice file", err)
	}

	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(noticeID + "\n"); err != nil {
		return fileFailure("append applied notice", err)
	}

	return nil
}

func noticeNumber(value string) (int, bool) {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}

	return number, true
}

func fileFailure(operation string, err error) error {
	wrapped := fmt.Errorf("%s: %w", operation, err)
	slog.Error(
		"notice file operation failed",
		slog.String("operation", operation),
		slog.Any("err", wrapped),
	)

	return wrapped
}

func stderr(text string) {
	_, _ = os.Stderr.WriteString(text)
}

func stdout(text string) {
	_, _ = os.Stdout.WriteString(text)
}

func failureStatus(err error) int {
	if err != nil {
		stderr("go-mk: " + err.Error() + "\n")

		return 1
	}

	return 0
}
