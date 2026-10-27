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

type makeReplayArguments struct {
	arguments []string
	remaining []string
	hasFile   bool
}

type makeDefaultFile string

type makeLongArgumentOption struct {
	name       string
	takesValue bool
	directory  bool
	file       bool
}

var makeLongArgumentOptions = []makeLongArgumentOption{
	{name: "directory", takesValue: true, directory: true},
	{name: "file", takesValue: true, file: true},
	{name: "makefile", takesValue: true, file: true},
	{name: "include-dir", takesValue: true},
	{name: "old-file", takesValue: true},
	{name: "assume-old", takesValue: true},
	{name: "what-if", takesValue: true},
	{name: "new-file", takesValue: true},
	{name: "assume-new", takesValue: true},
	{name: "eval", takesValue: true},
	{name: "jobserver-auth", takesValue: true},
	{name: "jobserver-fds", takesValue: true},
	{name: "jobs"},
	{name: "load-average"},
	{name: "max-load"},
	{name: "output-sync"},
	{name: "debug"},
	{name: "shuffle"},
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
	replay, err := makeContractArguments(invocation.arguments[1:])
	if err != nil {
		return err
	}
	var defaultFile makeDefaultFile
	if !replay.hasFile {
		defaultFile, err = defaultMakeContractFile()
		if err != nil {
			return err
		}
	}
	replayArguments := finishMakeContractArguments(replay, defaultFile, stopPath)
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
func makeContractArguments(arguments []string) (makeReplayArguments, error) {
	result := makeReplayArguments{arguments: make([]string, 0, len(arguments)+7)}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			result.remaining = arguments[index:]
			return result, nil
		}
		option := makeContractOption(argument)
		if option.next {
			if index+1 >= len(arguments) {
				return makeReplayArguments{}, fmt.Errorf("a Make option requires a value")
			}
			index++
			option.value = arguments[index]
		}
		if option.file && option.value == "-" {
			return makeReplayArguments{}, fmt.Errorf("recipe validation requires a saved Makefile; replace the standard-input Makefile with a file path")
		}
		result.hasFile = result.hasFile || option.file
		if option.directory {
			if option.retained != "" {
				result.arguments = append(result.arguments, option.retained)
			}
			continue
		}
		result.arguments = append(result.arguments, argument)
		if option.next {
			result.arguments = append(result.arguments, option.value)
		}
	}
	return result, nil
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
		if option, found := makeContractLongOption(name); found {
			return makeArgumentOption{directory: option.directory, file: option.file, next: option.takesValue && !inline, value: value}
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

func makeContractLongOption(name string) (makeLongArgumentOption, bool) {
	if name == "" {
		return makeLongArgumentOption{}, false
	}
	var selected makeLongArgumentOption
	matchCount := 0
	for _, option := range makeLongArgumentOptions {
		if option.name == name {
			return option, true
		}
		if strings.HasPrefix(option.name, name) {
			selected = option
			matchCount++
		}
	}
	return selected, matchCount == 1
}

func finishMakeContractArguments(replay makeReplayArguments, defaultFile makeDefaultFile, stopPath string) []string {
	result := replay.arguments
	if !replay.hasFile {
		result = append(result, "-f", string(defaultFile))
	}
	result = append(result, "_GO_MK_VALIDATE_PARSE=1", "_GO_MK_PROVISIONED=1", "-j1", "-f", stopPath)
	return append(result, replay.remaining...)
}

func defaultMakeContractFile() (makeDefaultFile, error) {
	for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
		info, err := os.Stat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return makeDefaultFile(name), nil
		}
	}
	return "", fmt.Errorf("cannot find the default Makefile for recipe validation; pass its path with -f")
}

func findMakeRecipeConflict(output string, sharedFiles []string) error {
	shared := make(map[string]bool, len(sharedFiles))
	for _, path := range sharedFiles {
		resolved, err := makeContractPath(path)
		if err != nil {
			return err
		}
		shared[resolved] = true
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
		newShared, err := makeContractSharedLocation(replacement.path, shared)
		if err != nil {
			return err
		}
		oldShared, err := makeContractSharedLocation(location.path, shared)
		if err != nil {
			return err
		}
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

func makeContractSharedLocation(path string, shared map[string]bool) (bool, error) {
	if path == "<builtin>" {
		return false, nil
	}
	resolved, err := makeContractPath(path)
	if err != nil {
		return false, err
	}
	return shared[resolved], nil
}

func makeContractPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return physical, nil
}
