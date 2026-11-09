package staticcheck

import (
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/analysis"
)

// FilenamePrefixAnalyzer reports sibling files that repeat a filename prefix.
var FilenamePrefixAnalyzer = &analysis.Analyzer{
	Name: "filenameprefix",
	Doc:  "requires a shared directory for repeated filename prefixes",
	Run:  runFilenamePrefix,
}

const minimumPrefix = 3

// Go's filename constraints include past and present build targets.
var (
	goSystems = strings.Fields(
		"aix android darwin dragonfly freebsd hurd illumos ios js linux nacl " +
			"netbsd openbsd plan9 solaris wasip1 windows zos",
	)
	goProcessors = strings.Fields(
		"386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 mips64le " +
			"mips64p32 mips64p32le ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm",
	)
)

type siblingFile struct {
	name string
	stem string
}

func runFilenamePrefix(pass *analysis.Pass) (any, error) {
	visited := make(map[string]bool)

	for _, file := range pass.Files {
		path := pass.Fset.File(file.Pos()).Name()
		if isGoBuildCacheDescriptorPath(path) {
			path = fileName(pass, file.Name.Pos())
		}

		directory := filepath.Dir(path)
		if filepath.Ext(path) != ".go" || visited[directory] {
			continue
		}

		visited[directory] = true
		if err := reportSiblingFiles(pass, directory); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func reportSiblingFiles(pass *analysis.Pass, directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		slog.Error(
			"list sibling Go files",
			slog.String("directory", directory),
			slog.Any("err", err),
		)

		return fmt.Errorf("list sibling Go files: %w", err)
	}

	files := make([]siblingFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasPrefix(name, "_") ||
			strings.HasPrefix(name, ".") {
			continue
		}

		files = append(files, siblingFile{name: name, stem: filenameStem(name)})
	}

	for _, file := range files {
		prefix := directoryPrefix(file.stem, files)
		if prefix == "" {
			continue
		}

		path := filepath.Join(directory, file.name)
		source := token.NewFileSet()
		syntax, err := parser.ParseFile(source, path, nil, parser.PackageClauseOnly)
		if err != nil {
			slog.Error("read sibling Go package", slog.String("path", path), slog.Any("err", err))

			return fmt.Errorf("read sibling Go package: %w", err)
		}

		// Filename diagnostics use physical positions even with //line directives.
		parsed := source.File(syntax.Name.Pos())
		physical := pass.Fset.AddFile(path, -1, parsed.Size())
		physical.SetLines(parsed.Lines())
		position := physical.Pos(parsed.Offset(syntax.Name.Pos()))
		pass.Reportf(
			position,
			"filenameprefix: %s repeats prefix %q; use directory %q",
			file.name,
			prefix,
			prefix,
		)
	}

	return nil
}

func directoryPrefix(stem string, files []siblingFile) string {
	var prefix string
	first := []rune(stem)

	for _, other := range files {
		if other.stem == stem {
			continue
		}

		second := []rune(other.stem)
		count := 0

		for count < len(first) && count < len(second) && first[count] == second[count] {
			count++
		}

		common := string(first[:count])
		split := strings.LastIndexAny(common, "_-.")
		if split >= 0 && utf8.RuneCountInString(common[:split]) >= minimumPrefix {
			common = common[:split]
		}

		common = strings.TrimRight(common, "_-.")
		if utf8.RuneCountInString(common) >= minimumPrefix &&
			(prefix == "" || len(common) < len(prefix)) {
			prefix = common
		}
	}

	return prefix
}

func filenameStem(name string) string {
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	stem = trimPlatformSuffix(stem, goProcessors)

	return trimPlatformSuffix(stem, goSystems)
}

func trimPlatformSuffix(stem string, platforms []string) string {
	prefix, suffix, found := strings.CutLast(stem, "_")
	if found && slices.Contains(platforms, suffix) {
		return prefix
	}

	return stem
}
