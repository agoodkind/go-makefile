package staticcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

const (
	fixFlagName   = "fix"
	diffFlagName  = "diff"
	jsonFlagName  = "json"
	testFlagName  = "test"
	flagsFlagName = "flags"
)

var (
	errArgumentAnalysis    = errors.New("argument analysis failed before applying fixes")
	errUnknownAnalyzerFlag = errors.New("unknown analyzer flag")
	errAnalyzerFlagValue   = errors.New("analyzer flag requires a value")
)

type driverFlag struct {
	Name string `json:"Name"`
	Bool bool   `json:"Bool"`
}

type argumentFixOptions struct {
	fix        bool
	diff       bool
	tests      bool
	query      bool
	queryFlags bool
	inspect    []string
	patterns   []string
}

func argumentFixRequested(arguments []string) bool {
	shortFix, longFix := "-"+fixFlagName, "--"+fixFlagName

	for _, argument := range arguments {
		bare := argument == shortFix || argument == longFix
		valued := strings.HasPrefix(argument, shortFix+"=") || strings.HasPrefix(argument, longFix+"=")
		if bare || valued {
			return true
		}
	}

	return false
}

func fixGeneratedArguments(arguments []string) error {
	if !argumentFixRequested(arguments) {
		return nil
	}

	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate argument fix driver: %w", err)
	}

	flags, err := nativeDriverFlags(binary)
	if err != nil {
		return err
	}

	options, err := parseArgumentFixOptions(arguments, flags)
	if err != nil {
		return err
	}

	if !options.fix || options.query || options.queryFlags {
		return nil
	}

	command := exec.CommandContext(context.Background(), binary, append([]string{"-json=true"}, options.inspect...)...)
	var output, diagnostics bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostics
	if err := command.Run(); err != nil {
		return fmt.Errorf("inspect selected argument fixes: %w\n%s", err, diagnostics.String())
	}

	var packagesByName map[string]map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &packagesByName); err != nil {
		return fmt.Errorf("decode selected argument fixes: %w", err)
	}

	active := false

	for _, analyzers := range packagesByName {
		if result, found := analyzers[ArgumentLinesAnalyzer.Name]; found {
			var findings []json.RawMessage
			err := json.Unmarshal(result, &findings)
			if err != nil {
				return fmt.Errorf("%w: %s", errArgumentAnalysis, result)
			}

			active = active || len(findings) > 0
		}
	}

	if !active {
		return nil
	}

	paths, err := generatedArgumentFiles(options.patterns, options.tests)
	if err != nil {
		return err
	}

	if len(paths) == 0 {
		return nil
	}

	if !options.diff {
		return FormatArguments(paths)
	}

	formatted, err := prepareArgumentFormatting(paths)
	if err != nil {
		return err
	}

	for _, path := range formatted.selected {
		if bytes.Equal(formatted.original[path], formatted.contents[path]) {
			continue
		}

		err := printArgumentDiff(path, formatted.original[path], formatted.contents[path])
		if err != nil {
			return err
		}
	}

	return nil
}

func nativeDriverFlags(binary string) (map[string]bool, error) {
	output, err := exec.CommandContext(context.Background(), binary, "-flags").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("read native analyzer flags: %w\n%s", err, output)
	}

	var listed []driverFlag
	if err := json.Unmarshal(output, &listed); err != nil {
		return nil, fmt.Errorf("decode native analyzer flags: %w", err)
	}

	flags := map[string]bool{fixFlagName: true, "debug": false, "cpuprofile": false, "memprofile": false, "trace": false}

	for _, flag := range listed {
		flags[flag.Name] = flag.Bool
	}

	return flags, nil
}

func parseArgumentFixOptions(arguments []string, flags map[string]bool) (argumentFixOptions, error) {
	options := argumentFixOptions{tests: true}

	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" || !strings.HasPrefix(argument, "-") || argument == "-" {
			options.inspect = append(options.inspect, arguments[index:]...)
			if argument == "--" {
				index++
			}

			options.patterns = arguments[index:]

			break
		}

		name, value, explicit := strings.Cut(strings.TrimLeft(argument, "-"), "=")
		if name == "h" || name == "help" || name == "V" {
			options.query = true
		}

		boolean, known := flags[name]
		if !known && !options.query {
			return argumentFixOptions{}, fmt.Errorf("%w -%s", errUnknownAnalyzerFlag, name)
		}

		handled, err := options.booleanFlag(name, value, explicit)
		if err != nil {
			return argumentFixOptions{}, err
		}

		if handled {
			continue
		}

		options.inspect = append(options.inspect, argument)
		if known && !boolean && !explicit {
			index++
			if index == len(arguments) {
				return argumentFixOptions{}, fmt.Errorf("-%s: %w", name, errAnalyzerFlagValue)
			}

			options.inspect = append(options.inspect, arguments[index])
		}
	}

	return options, nil
}

func (options *argumentFixOptions) booleanFlag(name, value string, explicit bool) (bool, error) {
	if name != fixFlagName && name != diffFlagName && name != jsonFlagName &&
		name != testFlagName && name != flagsFlagName {
		return false, nil
	}

	if !explicit {
		value = "true"
	}

	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid -%s value: %w", name, err)
	}

	switch name {
	case fixFlagName:
		options.fix = enabled
	case diffFlagName:
		options.diff = enabled
	case testFlagName:
		options.tests = enabled
	case flagsFlagName:
		options.queryFlags = enabled
	}

	return name != testFlagName && name != flagsFlagName, nil
}

func generatedArgumentFiles(patterns []string, tests bool) ([]string, error) {
	loaded, err := packages.Load(&packages.Config{Mode: packages.LoadSyntax, Tests: tests}, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load generated argument sources: %w", err)
	}

	selected := make(map[string]bool)

	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("load generated argument source package %s: %w", pkg.PkgPath, pkg.Errors[0])
		}

		sources, err := originalSources(&analysis.Pass{Fset: pkg.Fset, Files: pkg.Syntax})
		if err != nil {
			return nil, err
		}

		for _, source := range sources {
			if ast.IsGenerated(source.file) {
				selected[source.path] = true
			}
		}
	}

	paths := make([]string, 0, len(selected))
	for path := range selected {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	return paths, nil
}

func printArgumentDiff(path string, original, formatted []byte) error {
	oldLines := strings.Split(strings.TrimSuffix(string(original), "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(string(formatted), "\n"), "\n")
	var output strings.Builder
	fmt.Fprintf(&output, "--- %s (old)\n+++ %s (new)\n@@ -1,%d +1,%d @@\n", path, path, len(oldLines), len(newLines))

	for _, line := range oldLines {
		output.WriteString("-" + line + "\n")
	}

	if !bytes.HasSuffix(original, []byte("\n")) {
		output.WriteString("\\ No newline at end of file\n")
	}

	for _, line := range newLines {
		output.WriteString("+" + line + "\n")
	}

	if _, err := os.Stdout.WriteString(output.String()); err != nil {
		return fmt.Errorf("write generated argument diff: %w", err)
	}

	return nil
}
