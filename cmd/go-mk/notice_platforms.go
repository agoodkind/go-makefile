package main

import (
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The notice runs inside one analysis pass. That pass does not analyze a test
// file that builds only for another platform. A cross pass runs with cgo off
// and can report type errors in files that the first pass builds.
func addOtherPlatformFindings(findingsPath string) error {
	current := currentPlatform()
	targets := otherNoticePlatforms(current)
	if len(targets) == 0 {
		return nil
	}
	builtFiles, err := currentTestFiles()
	if err != nil {
		return err
	}
	merged, err := readFileLines(findingsPath)
	if err != nil {
		return err
	}
	for _, target := range targets {
		suffix := target.goos + "-" + target.goarch
		rawPath := filepath.Join(makeDir, "staticcheck-extra-scope-baseline."+suffix+".raw.out")
		targetPath := filepath.Join(makeDir, "staticcheck-extra-scope-baseline."+suffix+".out")
		saved := activePlatform
		activePlatform = target
		captureErr := staticcheckCaptureFindings(rawPath, targetPath)
		activePlatform = saved
		if captureErr != nil {
			return captureErr
		}
		lines, err := readFileLines(targetPath)
		if err != nil {
			return err
		}
		for _, line := range lines {
			file, _, found := strings.Cut(line, ":")
			if found && !builtFiles[file] {
				merged = append(merged, line)
			}
		}
	}
	return writeFindingsFile(findingsPath, sortedUnique(merged))
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

// A finding line starts with a path relative to the lint root.
func currentTestFiles() (map[string]bool, error) {
	slog.Info("notice list test files", slog.String("platform", currentPlatform().label()))
	command := exec.Command(
		"go", "list", "-e",
		"-f", `{{$dir := .Dir}}{{range .TestGoFiles}}{{$dir}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$dir}}/{{.}}{{"\n"}}{{end}}`,
		"./...",
	)
	command.Env = lintEnv()
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	// go list and the working directory can name the same directory through
	// different symbolic links, as with /var and /private/var on macOS.
	root, err := filepath.EvalSymlinks(lintRoot())
	if err != nil {
		return nil, err
	}
	files := make(map[string]bool)
	for _, path := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if path == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			continue
		}
		files[filepath.ToSlash(relative)] = true
	}
	return files, nil
}
