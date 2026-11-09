package multilineargs

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	goFileExtension       = ".go"
	recursivePathSuffix   = "/..."
	recursivePathWildcard = "..."
	currentDirectory      = "."
)

// FormatPaths formats Go calls in files or directory trees without loading packages or checking types.
func FormatPaths(paths []string) error {
	if len(paths) == 0 {
		return errors.New("call formatting requires at least one source path")
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve call-formatting directory: %w", err)
	}

	for _, path := range paths {
		if err := formatInput(path, workingDirectory); err != nil {
			return err
		}
	}

	return nil
}

func formatInput(path, workingDirectory string) error {
	if path == "" {
		return errors.New("call formatting requires a nonempty source path")
	}

	if path == recursivePathWildcard {
		path = currentDirectory
	} else if strings.HasSuffix(filepath.ToSlash(path), recursivePathSuffix) {
		path = strings.TrimSuffix(path, recursivePathWildcard)
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDirectory, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect call-formatting path %s: %w", path, err)
	}

	if info.IsDir() {
		if err := filepath.WalkDir(path, walkFormatter()); err != nil {
			return fmt.Errorf("format call arguments under %s: %w", path, err)
		}

		return nil
	}

	if filepath.Ext(path) != goFileExtension {
		return fmt.Errorf("call formatting requires a Go file or directory: %s", path)
	}

	return formatFile(path)
}

func walkFormatter() fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return fmt.Errorf("read call-formatting path %s: %w", path, walkError)
		}

		if entry.IsDir() || filepath.Ext(path) != goFileExtension {
			return nil
		}

		return formatFile(path)
	}
}

func formatFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect source file %s: %w", path, err)
	}

	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read source file %s: %w", path, err)
	}

	files := token.NewFileSet()
	file, err := parser.ParseFile(
		files,
		path,
		source,
		parser.ParseComments|parser.SkipObjectResolution,
	)
	if err != nil {
		return fmt.Errorf("parse source file %s: %w", path, err)
	}

	if ast.IsGenerated(file) {
		return nil
	}

	formatted, err := formatCallArguments(file, files.File(file.Pos()), source)
	if err != nil {
		return fmt.Errorf("format source file %s: %w", path, err)
	}

	if bytes.Equal(source, formatted) {
		return nil
	}

	if err := os.WriteFile(path, formatted, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write source file %s: %w", path, err)
	}

	slog.Debug("source file formatted", "path", path)

	return nil
}
