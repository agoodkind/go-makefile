package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

//go:embed makecontract-stop.mk
var makeContractStop []byte

const makeContractSentinel = "go-mk-make-parse-complete"

const makeContractRemediation = "If this is a new error, please correct it now by moving the consumer recipe to a separate target and updating its callers"

type makeContractOptions struct {
	processID int
	shared    []string
}

type makeProcessInvocation struct {
	executable  string
	arguments   []string
	environment []string
}

type makeContractFlag string

const (
	makeContractPIDFlag    makeContractFlag = "--make-pid"
	makeContractSharedFlag makeContractFlag = "--shared-file"
)

type makeRecipeLocation struct {
	path   string
	line   string
	target string
	old    bool
}

var makeRecipeWarning = regexp.MustCompile("(?m)^(.+):([0-9]+): warning: (overriding|ignoring old) (?:commands|recipe) for target [`'](.+)'\\r?$")

func runValidateMake(arguments []string) int {
	options, err := parseMakeContractOptions(arguments)
	if err == nil {
		err = validateMakeContract(options)
	}
	if err != nil {
		writeStderr("go-makefile: " + err.Error() + "\n")
		return 1
	}
	writeStdout("ok\n")
	return 0
}

func parseMakeContractOptions(arguments []string) (makeContractOptions, error) {
	options := makeContractOptions{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if index+1 >= len(arguments) {
			return options, &missingValueError{option: argument}
		}
		index++
		value := arguments[index]
		switch makeContractFlag(argument) {
		case makeContractPIDFlag:
			identifier, err := strconv.Atoi(value)
			if err != nil || identifier <= 0 {
				return options, fmt.Errorf("--make-pid requires a positive process ID")
			}
			options.processID = identifier
		case makeContractSharedFlag:
			options.shared = append(options.shared, value)
		default:
			return options, &unknownOptionError{option: argument}
		}
	}
	if options.processID == 0 || len(options.shared) == 0 {
		return options, fmt.Errorf("validate-make requires --make-pid and --shared-file")
	}
	return options, nil
}

func validateMakeContract(options makeContractOptions) error {
	slog.Debug("validate shared Make recipes", slog.Int("shared_files", len(options.shared)))
	invocation, err := readMakeProcess(options.processID)
	if err != nil {
		slog.Error("read Make invocation failed", slog.Any("err", err))
		return fmt.Errorf("cannot read the Make invocation for recipe validation: %w", err)
	}
	if len(invocation.arguments) == 0 {
		return fmt.Errorf("cannot validate an empty Make invocation")
	}
	stopFile, err := os.CreateTemp("", "go-mk-contract-*.mk")
	if err != nil {
		return fmt.Errorf("cannot create the Make validation file: %w", err)
	}
	stopPath := stopFile.Name()
	defer func() { _ = os.Remove(stopPath) }()
	if _, err := stopFile.Write(makeContractStop); err != nil {
		_ = stopFile.Close()
		return fmt.Errorf("cannot write the Make validation file: %w", err)
	}
	if err := stopFile.Close(); err != nil {
		return fmt.Errorf("cannot close the Make validation file: %w", err)
	}
	replayArguments, err := makeContractArguments(invocation.arguments[1:], stopPath)
	if err != nil {
		return err
	}
	command := exec.Command(invocation.executable, replayArguments...)
	command.Env = setEnvVar(invocation.environment, "LC_ALL", "C")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	runErr := command.Run()
	if err := findMakeRecipeConflict(output.String(), options.shared); err != nil {
		return err
	}
	if runErr == nil || !strings.Contains(output.String(), makeContractSentinel) {
		return fmt.Errorf("the verification parse did not finish; correct Makefile parse errors before running targets")
	}
	return nil
}

// makeContractArguments preserves argument boundaries while removing directory
// options because the running Make process has already changed directories.
func makeContractArguments(arguments []string, stopPath string) ([]string, error) {
	result := make([]string, 0, len(arguments)+7)
	hasFile := false
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			return finishMakeContractArguments(result, hasFile, stopPath, arguments[index:])
		}
		option := makeContractOption(argument)
		if option.next {
			if index+1 >= len(arguments) {
				return nil, fmt.Errorf("a Make option requires a value")
			}
			index++
			option.value = arguments[index]
		}
		if option.file && option.value == "-" {
			return nil, fmt.Errorf("recipe validation requires a saved Makefile; replace the standard-input Makefile with a file path")
		}
		hasFile = hasFile || option.file
		if option.directory {
			if option.retained != "" {
				result = append(result, option.retained)
			}
			continue
		}
		result = append(result, argument)
		if option.next {
			result = append(result, option.value)
		}
	}
	return finishMakeContractArguments(result, hasFile, stopPath, nil)
}

type makeArgumentOption struct {
	directory bool
	file      bool
	next      bool
	value     string
	retained  string
}

func makeContractOption(argument string) makeArgumentOption {
	if strings.HasPrefix(argument, "--") {
		name, value, inline := strings.Cut(argument[2:], "=")
		for _, option := range []string{"directory", "file", "makefile", "include-dir", "old-file", "assume-old", "what-if", "new-file", "assume-new", "eval", "jobserver-auth", "jobserver-fds"} {
			if name != "" && strings.HasPrefix(option, name) {
				return makeArgumentOption{directory: option == "directory", file: option == "file" || option == "makefile", next: !inline, value: value}
			}
		}
		return makeArgumentOption{}
	}
	if !strings.HasPrefix(argument, "-") {
		return makeArgumentOption{}
	}
	for offset := 1; offset < len(argument); offset++ {
		switch argument[offset] {
		case 'C', 'f', 'I', 'o', 'W', 'E':
			retained := argument[:offset]
			if retained == "-" {
				retained = ""
			}
			value := argument[offset+1:]
			return makeArgumentOption{directory: argument[offset] == 'C', file: argument[offset] == 'f', next: value == "", value: value, retained: retained}
		case 'j', 'l', 'O':
			return makeArgumentOption{}
		}
	}
	return makeArgumentOption{}
}

func finishMakeContractArguments(result []string, hasFile bool, stopPath string, remaining []string) ([]string, error) {
	if !hasFile {
		defaultFile, err := defaultMakeContractFile()
		if err != nil {
			return nil, err
		}
		result = append(result, "-f", defaultFile)
	}
	result = append(result, "_GO_MK_VALIDATE_PARSE=1", "_GO_MK_PROVISIONED=1", "-j1", "-f", stopPath)
	return append(result, remaining...), nil
}

func defaultMakeContractFile() (string, error) {
	for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			return name, nil
		}
	}
	return "", fmt.Errorf("cannot find the default Makefile for recipe validation; pass its path with -f")
}

func findMakeRecipeConflict(output string, sharedFiles []string) error {
	shared := make(map[string]bool, len(sharedFiles))
	for _, path := range sharedFiles {
		shared[makeContractPath(path)] = true
	}
	var replacement makeRecipeLocation
	for _, match := range makeRecipeWarning.FindAllStringSubmatch(output, -1) {
		location := makeRecipeLocation{
			path: match[1], line: match[2], target: match[4], old: match[3] == "ignoring old",
		}
		if !location.old {
			replacement = location
			continue
		}
		if replacement.target != location.target {
			continue
		}
		newShared := shared[makeContractPath(replacement.path)]
		oldShared := shared[makeContractPath(location.path)]
		if newShared == oldShared {
			continue
		}
		consumer := replacement
		if newShared {
			consumer = location
		}
		return fmt.Errorf("%s:%s replaces the shared recipe for target %q. %s", consumer.path, consumer.line, consumer.target, makeContractRemediation)
	}
	return nil
}

func makeContractPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return physical
	}
	return absolute
}
