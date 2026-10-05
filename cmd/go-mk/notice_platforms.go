package main

import (
	"fmt"
	"log/slog"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
)

// runStaticcheckExtra applies notice 2 during the analysis of one platform: the
// host, or the first GO_MK_PLATFORMS target. That analysis skips each test file
// with a build constraint for another platform. addOtherPlatformFindings
// analyzes only the packages that contain such files, and adds only the
// findings in those files.
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
			strings.Join(packages, " "), target.label(), status, rawPath, target.label())
		slog.Error("notice analyze other platform", slog.String("platform", target.label()), slog.String("err", buildErr.Error()))
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

// testFilesFor maps each test file that the platform builds to its package
// directory. staticcheckCaptureFindings writes each finding with a file path
// relative to lintRoot, and the keys and values use that form.
func testFilesFor(platform platformTarget) (map[string]string, error) {
	slog.Info("notice list test files", slog.String("platform", platform.label()))
	command := exec.Command(
		"go", "list", "-e",
		"-f", `{{$dir := .Dir}}{{range .TestGoFiles}}{{$dir}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$dir}}/{{.}}{{"\n"}}{{end}}`,
		"./...",
	)
	saved := activePlatform
	activePlatform = platform
	command.Env = lintEnv()
	activePlatform = saved
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	// go list and lintRoot can return different paths for one directory when
	// the path contains a symbolic link, as with /var and /private/var on macOS.
	root, err := filepath.EvalSymlinks(lintRoot())
	if err != nil {
		return nil, err
	}
	files := make(map[string]string)
	for _, listed := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if listed == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(listed)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			continue
		}
		file := filepath.ToSlash(relative)
		files[file] = path.Dir(file)
	}
	return files, nil
}
