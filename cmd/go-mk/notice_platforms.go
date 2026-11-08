package main

import (
	"fmt"
	"log/slog"
	"runtime"
	"strings"

	"goodkind.io/go-makefile/internal/notice"
)

// A notice also analyzes source and test files that only another platform builds.
func addOtherPlatformFindings(findingsPath string) error {
	current := currentPlatform()
	targets := otherNoticePlatforms(current)
	if len(targets) == 0 {
		return nil
	}

	builtFiles, err := testFilesFor(current)
	if err != nil {
		return err
	}

	merged, err := readFileLines(findingsPath)
	if err != nil {
		return err
	}

	for _, target := range targets {
		lines, err := otherPlatformTestFindings(target, builtFiles)
		if err != nil {
			return err
		}

		merged = append(merged, lines...)
	}

	return writeFindingsFile(findingsPath, sortedUnique(merged))
}

func otherPlatformTestFindings(target platformTarget, builtFiles map[string]string) ([]string, error) {
	findings, err := notice.OtherPlatformFindings(target.goos, target.goarch, builtFiles, staticcheckSelectedBin, staticcheckFlagsText, hostLintEnv, lintRoot)
	if err != nil {
		wrapped := fmt.Errorf("analyze other-platform source: %w", err)
		slog.Error("analyze other-platform source", slog.Any("err", wrapped))

		return nil, wrapped
	}

	return findings, nil
}

func currentPlatform() platformTarget {
	if activePlatform.goos != "" {
		return activePlatform
	}

	return platformTarget{goos: runtime.GOOS, goarch: runtime.GOARCH}
}

func otherNoticePlatforms(current platformTarget) []platformTarget {
	targets := make([]platformTarget, 0, 4)

	for _, entry := range platformStubPlatforms() {
		goos, goarch, ok := strings.Cut(entry, "/")
		target := platformTarget{goos: goos, goarch: goarch}
		if !ok || target == current {
			continue
		}

		targets = append(targets, target)
	}

	return targets
}

// testFilesFor lists Go source and test files for a notice platform.
func testFilesFor(platform platformTarget) (map[string]string, error) {
	files, err := notice.SourceFiles(platform.goos, platform.goarch, hostLintEnv, lintRoot)
	if err != nil {
		wrapped := fmt.Errorf("list platform source: %w", err)
		slog.Error("list platform source", slog.Any("err", wrapped))

		return nil, wrapped
	}

	return files, nil
}
