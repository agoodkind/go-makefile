package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/notice"
)

// addOtherPlatformFindings includes findings in source files and tests that
// build only for another declared platform.
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

func otherPlatformTestFindings(
	target platformTarget,
	builtFiles map[string]string,
) ([]string, error) {
	targetFiles, err := testFilesFor(target)
	if err != nil {
		return nil, err
	}

	onlyTarget := make(map[string]bool)
	packageDirs := make(map[string]bool)

	for file, dir := range targetFiles {
		if _, built := builtFiles[file]; built {
			continue
		}

		onlyTarget[file] = true
		packageDirs["./"+dir] = true
	}

	if len(packageDirs) == 0 {
		return nil, nil
	}

	selected, err := staticcheckSelectedBin()
	if err != nil {
		return nil, err
	}

	if selected == "" || !isExecutable(selected) {
		return nil, nil
	}

	packages := make([]string, 0, len(packageDirs))
	for dir := range packageDirs {
		packages = append(packages, dir)
	}

	sort.Strings(packages)
	args := append(splitWords(staticcheckFlagsText()), packages...)
	suffix := target.goos + "-" + target.goarch
	rawPath := filepath.Join(makeDir, "staticcheck-extra-scope-baseline."+suffix+".raw.out")
	saved := activePlatform
	activePlatform = target
	status, err := captureCommand(selected, args, rawPath)
	activePlatform = saved
	if err != nil {
		return nil, err
	}

	if status != 0 && status != staticcheckDiagnosticsStatus {
		buildErr := fmt.Errorf(
			"staticcheck-extra cannot build %s for %s on this host (exit status %d, output in %s). Run make check on a %s host",
			strings.Join(packages, " "),
			target.label(),
			status,
			rawPath,
			target.label(),
		)
		slog.Error(
			"notice analyze other platform",
			slog.String("platform", target.label()),
			slog.String("err", buildErr.Error()),
		)

		return nil, buildErr
	}

	rawLines, err := readFileLines(rawPath)
	if err != nil {
		return nil, err
	}

	root := lintRoot()

	kept := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		normalized := findings.NormalizePath(line, root, root)
		file, _, found := strings.Cut(normalized, ":")
		if found && onlyTarget[file] {
			kept = append(kept, normalized)
		}
	}

	return kept, nil
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

// testFilesFor includes source files and tests selected by the platform.
func testFilesFor(platform platformTarget) (map[string]string, error) {
	return notice.Files(lintRoot(), platform.goos, platform.goarch, hostLintEnv())
}
