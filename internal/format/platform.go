package format

import (
	"context"
	"fmt"
	"go/build"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type platform struct {
	goos   string
	goarch string
	files  []string
}

func sourcePlatforms(files, tags []string) ([]platform, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, failure("resolve formatting directory", err)
	}

	context := build.Default
	context.BuildTags = append(context.BuildTags, tags...)
	platforms := []platform{{goos: context.GOOS, goarch: context.GOARCH, files: nil}}
	var groups []platform

	for _, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			return nil, failure("resolve source path", err)
		}

		relative, err := filepath.Rel(root, absolute)
		if err != nil {
			return nil, failure("resolve relative source path", err)
		}

		matched := false

		for index := 0; index < len(platforms); index++ {
			candidate := platforms[index]
			context.GOOS, context.GOARCH = candidate.goos, candidate.goarch
			matches, err := context.MatchFile(filepath.Dir(file), filepath.Base(file))
			if err != nil {
				return nil, failure("select build platform for "+file, err)
			}

			if matches {
				groups = addPlatformFile(groups, candidate, filepath.ToSlash(relative))
				matched = true
				break
			}

			if len(platforms) == 1 {
				available, err := availablePlatforms()
				if err != nil {
					return nil, err
				}

				platforms = append(platforms, available...)
			}
		}

		if !matched {
			return nil, fmt.Errorf("selected source %s does not match any supported Go platform with the configured build tags; set run.build-tags to enable this file", file)
		}
	}

	return groups, nil
}

func availablePlatforms() ([]platform, error) {
	slog.Info("list Go formatting platforms")
	output, err := exec.CommandContext(context.Background(), "go", "tool", "dist", "list").CombinedOutput()
	if err != nil {
		return nil, failure(fmt.Sprintf("list Go build platforms: %s", output), err)
	}

	var platforms []platform

	for target := range strings.FieldsSeq(string(output)) {
		goos, goarch, valid := strings.Cut(target, "/")
		if !valid {
			return nil, fmt.Errorf("invalid Go build platform %q", target)
		}

		platforms = append(platforms, platform{goos: goos, goarch: goarch, files: nil})
	}

	return platforms, nil
}

func addPlatformFile(groups []platform, candidate platform, file string) []platform {
	for index := range groups {
		if groups[index].goos == candidate.goos && groups[index].goarch == candidate.goarch {
			groups[index].files = append(groups[index].files, file)
			return groups
		}
	}

	candidate.files = []string{file}

	return append(groups, candidate)
}
