package main

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func runGolangciCacheSaveDecision() int {
	primaryCommand := os.Getenv("GO_MK_CI_PRIMARY_COMMAND")
	primaryOutcome := os.Getenv("GO_MK_CI_PRIMARY_OUTCOME")
	golangciRan := os.Getenv("GO_MK_CI_GOLANGCI_RAN") == "true"
	cacheHit := os.Getenv("GO_MK_CI_LINT_CACHE_HIT") == "true"

	shouldVerify := primaryOutcome == "success" ||
		(primaryCommand == "make build-check" && primaryOutcome == "failure" && golangciRan)
	if cacheHit || !shouldVerify {
		return writeGolangciCacheSaveDecision(false)
	}

	directories, err := readGolangciCacheDirectories(os.Getenv("GO_MK_CI_LINT_CACHE_PATHS"))
	if err != nil {
		writeStderr("go-mk-golangci-cache: " + err.Error() + "\n")
		return 1
	}
	if len(directories) == 0 {
		writeStderr("go-mk-golangci-cache: no cache directory matched any configured path after " + primaryCommand + "\n")
		return 1
	}
	for _, directory := range directories {
		writeStdout("golangci-lint cache: " + directory + "\n")
	}
	return writeGolangciCacheSaveDecision(true)
}

func reportGolangciRunToCI() {
	if os.Getenv("GO_MK_CI_REPORT_GOLANGCI_RUN") != "true" {
		return
	}
	outputPath := os.Getenv("GITHUB_OUTPUT")
	output, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		writeStderr("go-mk-golangci-cache: report completed lint run: " + err.Error() + "\n")
		return
	}
	defer func() {
		_ = output.Close()
	}()
	if _, err := output.WriteString("golangci_ran=true\n"); err != nil {
		writeStderr("go-mk-golangci-cache: report completed lint run: " + err.Error() + "\n")
	}
}

func readGolangciCacheDirectories(patterns string) ([]string, error) {
	directories := make([]string, 0)
	seen := make(map[string]bool)
	for _, pattern := range strings.Split(patterns, "\n") {
		pattern = filepath.FromSlash(strings.TrimSuffix(pattern, "\r"))
		if pattern == "" {
			continue
		}
		matches, err := expandGolangciCachePattern(pattern)
		if err != nil {
			slog.Error("invalid golangci-lint cache path pattern", slog.String("pattern", pattern), slog.Any("error", err))
			return nil, fmt.Errorf("invalid cache path pattern %q: %w", pattern, err)
		}
		for _, match := range matches {
			info, statErr := os.Stat(match)
			if statErr == nil && info.IsDir() && !seen[match] {
				directories = append(directories, match)
				seen[match] = true
			}
		}
	}
	return directories, nil
}

func expandGolangciCachePattern(pattern string) ([]string, error) {
	separator := string(filepath.Separator)
	marker := separator + "**" + separator
	markerIndex := strings.Index(pattern, marker)
	if markerIndex < 0 {
		return filepath.Glob(pattern)
	}

	root := pattern[:markerIndex]
	suffix := pattern[markerIndex+len(marker):]
	matches := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root && os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		matched, matchErr := pathMatchesGolangciCacheSuffix(path, suffix)
		if matchErr != nil {
			return matchErr
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, err
}

func pathMatchesGolangciCacheSuffix(path string, suffix string) (bool, error) {
	pathParts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	suffixParts := strings.Split(filepath.Clean(suffix), string(filepath.Separator))
	if len(pathParts) < len(suffixParts) {
		return false, nil
	}
	pathParts = pathParts[len(pathParts)-len(suffixParts):]
	for i, suffixPart := range suffixParts {
		matched, err := filepath.Match(suffixPart, pathParts[i])
		if err != nil || !matched {
			return false, err
		}
	}
	return true, nil
}

func writeGolangciCacheSaveDecision(save bool) int {
	outputPath := os.Getenv("GITHUB_OUTPUT")
	if outputPath == "" {
		writeStderr("go-mk-golangci-cache: GITHUB_OUTPUT is required\n")
		return 1
	}
	output, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		writeStderr("go-mk-golangci-cache: open GITHUB_OUTPUT: " + err.Error() + "\n")
		return 1
	}
	defer func() {
		_ = output.Close()
	}()
	if _, err := fmt.Fprintf(output, "save=%t\n", save); err != nil {
		writeStderr("go-mk-golangci-cache: write GITHUB_OUTPUT: " + err.Error() + "\n")
		return 1
	}
	return 0
}
