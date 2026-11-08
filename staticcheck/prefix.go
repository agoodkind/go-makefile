package staticcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/analysis"
)

// FilenamePrefixAnalyzer requires directories for repeated filename
// prefixes.
var FilenamePrefixAnalyzer = &analysis.Analyzer{
	Name: "filenameprefix",
	Doc:  "requires a shared directory for sibling files that repeat a prefix",
	Run:  runFilenamePrefix,
}

const minimumFilenamePrefix = 3

const architectureSuffixes = "386 amd64 arm arm64 loong64 mips mips64 mips64le mipsle " +
	"ppc64 ppc64le riscv64 s390x wasm"

const operatingSystemSuffixes = "aix android darwin dragonfly freebsd illumos ios js linux " +
	"netbsd openbsd plan9 solaris wasip1 windows"

func runFilenamePrefix(pass *analysis.Pass) (any, error) {
	sources, err := originalSources(pass)
	if err != nil {
		return nil, err
	}

	directories := make(map[string]bool)

	for _, source := range sources {
		directories[filepath.Dir(source.path)] = true
	}

	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}

	sort.Strings(ordered)

	for _, directory := range ordered {
		err := reportFilenamePrefixes(pass, directory)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func reportFilenamePrefixes(pass *analysis.Pass, directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read filename prefix directory: %w", err)
	}

	sources := make(map[string]*ast.File)
	stems := make(map[string]bool)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}

		path := filepath.Join(directory, name)
		file, err := parser.ParseFile(pass.Fset, path, nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse filename prefix source: %w", err)
		}

		if isGeneratedTestMainFile(file, path) {
			continue
		}

		sources[name] = file
		stems[filenameStem(name)] = true
	}

	ordered := make([]string, 0, len(stems))
	for stem := range stems {
		ordered = append(ordered, stem)
	}

	sort.Strings(ordered)
	prefixes := filenameGroups(ordered)

	for _, entry := range entries {
		name := entry.Name()
		stem := filenameStem(name)
		prefix := prefixes[stem]
		file, found := sources[name]
		if prefix == "" || !found {
			continue
		}

		suffix := strings.TrimLeft(strings.TrimPrefix(stem, prefix), "_-")
		if suffix == "" {
			suffix = stem
		}

		message := "filenameprefix: %s repeats prefix %q; use directory %q and filename stem %q"
		reportAtf(pass, file, file.Name.Pos(), message, name, prefix, prefix, suffix)
	}

	return nil
}

func sharedFilenamePrefix(first, second string) string {
	firstRunes := []rune(first)
	secondRunes := []rune(second)

	count := 0
	for count < len(firstRunes) && count < len(secondRunes) && firstRunes[count] == secondRunes[count] {
		count++
	}

	prefix := string(firstRunes[:count])
	if separator := strings.LastIndexAny(prefix, "_-"); separator >= minimumFilenamePrefix {
		prefix = prefix[:separator]
	}

	prefix = strings.TrimRight(prefix, "_-")
	if utf8.RuneCountInString(prefix) < minimumFilenamePrefix {
		return ""
	}

	return prefix
}

func filenameStem(name string) string {
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	stem = trimPlatformSuffix(stem, architectureSuffixes)

	return trimPlatformSuffix(stem, operatingSystemSuffixes)
}

func trimPlatformSuffix(stem, platforms string) string {
	before, after, ok := strings.CutLast(stem, "_")
	if !ok {
		return stem
	}

	suffix := after

	if slices.Contains(strings.Fields(platforms), suffix) {
		return before
	}

	return stem
}

func filenameGroups(ordered []string) map[string]string {
	prefixes := make(map[string]string)

	for first, stem := range ordered {
		for _, other := range ordered[first+1:] {
			prefix := sharedFilenamePrefix(stem, other)
			if prefix == "" {
				continue
			}

			for _, candidate := range ordered {
				shorter := len(candidate) < len(prefix)
				shared := strings.HasPrefix(stem, candidate) && strings.HasPrefix(other, candidate)
				if utf8.RuneCountInString(candidate) >= minimumFilenamePrefix && shorter && shared {
					prefix = candidate
				}
			}

			for _, member := range []string{stem, other} {
				previous := prefixes[member]
				if previous == "" || len(prefix) < len(previous) {
					prefixes[member] = prefix
				}
			}
		}
	}

	return prefixes
}
