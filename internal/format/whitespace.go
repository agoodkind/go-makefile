package format

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"goodkind.io/go-makefile/internal/lint"
)

func whitespace(
	binary string,
	files []string,
	run func(string, []string) error,
) error {
	configuration, flags, tags, err := whitespaceConfiguration()
	if err != nil {
		return failure("prepare whitespace configuration", err)
	}
	defer removeTemporary(configuration)

	groups, err := sourcePlatforms(files, tags)
	if err != nil {
		return failure("select whitespace platforms", err)
	}

	for _, group := range groups {
		if err := whitespacePlatform(binary, configuration, flags, group, run); err != nil {
			return err
		}
	}

	return nil
}

func whitespacePlatform(
	binary string,
	configuration string,
	flags []string,
	group platform,
	run func(string, []string) error,
) error {
	patch, err := selectionPatch(group.files)
	if err != nil {
		return failure("prepare whitespace selection", err)
	}
	defer removeTemporary(patch)

	args := []string{"GOOS=" + group.goos, "GOARCH=" + group.goarch, binary, "run"}
	args = append(args, flags...)
	args = append(
		args,
		"--config",
		configuration,
		"--fix",
		"--enable-only=wsl_v5",
		"--tests=true",
		"--new-from-patch",
		patch,
		"--whole-files",
	)
	args = append(args, lint.ScopedPackagesFromFiles(group.files)...)
	if err := run("env", args); err != nil {
		return failure("format whitespace for "+group.goos+"/"+group.goarch, err)
	}

	return nil
}

func selectionPatch(files []string) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", failure("resolve formatting directory", err)
	}

	var patch strings.Builder

	for _, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			return "", failure("resolve source path", err)
		}

		path, err := filepath.Rel(root, absolute)
		if err != nil {
			return "", failure("resolve relative source path", err)
		}

		contents, err := os.ReadFile(absolute)
		if err != nil {
			return "", failure("read selected source", err)
		}

		lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
		fmt.Fprintf(&patch, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", filepath.ToSlash(path), len(lines))

		for _, line := range lines {
			patch.WriteString("+" + line + "\n")
		}
	}

	return temporaryFile("", "go-mk-whitespace-*.patch", []byte(patch.String()))
}

func temporaryFile(directory, pattern string, contents []byte) (string, error) {
	slog.Info("create temporary formatting file", slog.String("directory", directory))
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", failure("create formatting file", err)
	}

	_, writeErr := file.Write(contents)
	closeErr := file.Close()
	if writeErr != nil {
		removeTemporary(file.Name())
		return "", failure("write formatting file", writeErr)
	}

	if closeErr != nil {
		removeTemporary(file.Name())
		return "", failure("close formatting file", closeErr)
	}

	return file.Name(), nil
}

func removeTemporary(path string) {
	if err := os.Remove(path); err != nil {
		slog.Error("remove temporary formatting file", slog.String("path", path), slog.Any("err", err))
	}
}
