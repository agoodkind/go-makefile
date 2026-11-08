package notice

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"goodkind.io/go-makefile/internal/capture"
	"goodkind.io/go-makefile/internal/findings"
)

const staticcheckDiagnosticsStatus = 3

var (
	errMissingAnalyzer = errors.New("notice requires an executable staticcheck-extra analyzer")
	errTargetBuild     = errors.New("staticcheck-extra cannot build")
)

// OtherPlatformFindings analyzes source files unique to a target platform.
//
// A target build failure returns an error with the required platform and
// captured output path. The caller supplies analyzer and environment callbacks.
//
// The analysis follows this sequence:
//  1. SourceFiles lists the target platform's source files.
//  2. The analyzer checks packages with files outside builtFiles.
//  3. The result includes findings only from those additional files.
func OtherPlatformFindings(
	goos string,
	goarch string,
	builtFiles map[string]string,
	selectedBinary func() (string, error),
	flags func() string,
	environment func() []string,
	root func() string,
) ([]string, error) {
	targetFiles, err := SourceFiles(goos, goarch, environment, root)
	if err != nil {
		return nil, err
	}

	onlyTarget := make(map[string]bool)
	packageDirs := make(map[string]bool)

	for file, directory := range targetFiles {
		if _, built := builtFiles[file]; built {
			continue
		}

		onlyTarget[file] = true
		packageDirs["./"+directory] = true
	}

	if len(packageDirs) == 0 {
		return nil, nil
	}

	selected, err := selectedBinary()
	if err != nil {
		return nil, err
	}

	if selected == "" {
		return nil, errMissingAnalyzer
	}

	packages := make([]string, 0, len(packageDirs))
	for directory := range packageDirs {
		packages = append(packages, directory)
	}

	sort.Strings(packages)
	args := append(strings.Fields(flags()), packages...)
	rawPath := filepath.Join(".make", "staticcheck-extra-scope-baseline."+goos+"-"+goarch+".raw.out")
	result, err := capture.Run(selected, args, sourceEnvironment(environment(), goos, goarch), rawPath)
	if err != nil {
		return nil, failure("analyze notice source for "+goos+"/"+goarch, err)
	}

	if result.Status != 0 && result.Status != staticcheckDiagnosticsStatus {
		buildError := fmt.Errorf(
			"%w %s for %s/%s on this host (exit status %d, output in %s). "+
				"Run make check on a %s/%s host",
			errTargetBuild,
			strings.Join(packages, " "),
			goos,
			goarch,
			result.Status,
			rawPath,
			goos,
			goarch,
		)
		slog.Error("notice target build failed", slog.Any("err", buildError))

		return nil, buildError
	}

	return platformFindings(rawPath, root(), onlyTarget)
}

func platformFindings(rawPath, root string, selected map[string]bool) ([]string, error) {
	output, err := os.ReadFile(rawPath)
	if err != nil {
		return nil, failure("read other-platform notice findings", err)
	}

	kept := make([]string, 0)

	for line := range strings.SplitSeq(strings.TrimSuffix(string(output), "\n"), "\n") {
		normalized := findings.NormalizePath(line, root, root)
		file, _, found := strings.Cut(normalized, ":")
		if found && selected[file] {
			kept = append(kept, normalized)
		}
	}

	return kept, nil
}
