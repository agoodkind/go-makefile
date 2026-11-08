package format

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"goodkind.io/go-makefile/internal/capture"
)

var (
	errFlagValue        = errors.New("requires a value")
	errLinterSelection  = errors.New("golangci-lint linters exited")
	errWhitespaceStatus = errors.New("golangci-lint whitespace capture exited")
	selectionFlags      = []string{"--enable", "-E", "--disable", "-D", "--enable-only", "--default", "--fast-only"}
)

// CaptureCommand runs a command and records its output and exit status.
//
// When golangci-lint enables wsl_v5, a second run appends whitespace
// findings for files covered by the shared exclusions. Both runs use
// the caller's environment and package selection.
//
// For example, the capture for this command includes whitespace
// findings in test files:
//
//	golangci-lint run --enable-only=wsl_v5 ./...
func CaptureCommand(
	binary string,
	args []string,
	outputPath string,
	environment func() []string,
) (int, error) {
	env := environment()
	result, err := capture.Run(binary, args, env, outputPath)
	if err != nil {
		return 0, failure("capture command output", err)
	}

	linter := os.Getenv("GOLANGCI_LINT")
	if linter == "" {
		linter = "golangci-lint"
	}

	if binary != linter || len(args) == 0 || args[0] != golangciRunCommand {
		return result.Status, nil
	}

	query, remaining, err := linterFlags(args[1:])
	if err != nil {
		return 0, err
	}

	enabled, err := whitespaceEnabled(binary, query, env)
	if err != nil {
		return 0, err
	}

	if enabled {
		err = captureWhitespace(binary, remaining, env, outputPath)
		if err != nil {
			return 0, err
		}
	}

	return result.Status, nil
}

func linterFlags(arguments []string) ([]string, []string, error) {
	var query []string
	var remaining []string

	for index := 0; index < len(arguments); index++ {
		flag := arguments[index]
		name, _, joined := strings.Cut(flag, "=")
		if name == "--" {
			remaining = append(remaining, arguments[index:]...)

			break
		}

		config := name == "-c" || name == configFlag || name == noConfigFlag
		selection := slices.Contains(selectionFlags, name)
		exitCode := name == "--issues-exit-code"
		if !config && !selection && !exitCode {
			compact := strings.HasPrefix(flag, "-c") || strings.HasPrefix(flag, "-E") || strings.HasPrefix(flag, "-D")
			if compact {
				query = append(query, flag)
			}

			if !compact || strings.HasPrefix(flag, "-c") {
				remaining = append(remaining, flag)
			}

			continue
		}

		start := index
		if !joined && name != noConfigFlag && name != "--fast-only" {
			index++
			if index >= len(arguments) {
				return nil, nil, failure(flag, errFlagValue)
			}
		}

		if exitCode {
			continue
		}

		query = append(query, arguments[start:index+1]...)
		if config {
			remaining = append(remaining, arguments[start:index+1]...)
		}
	}

	return query, remaining, nil
}

func whitespaceEnabled(binary string, flags, env []string) (bool, error) {
	temporary, err := temporaryFile("", "go-mk-linters-*.json", nil)
	if err != nil {
		return false, err
	}
	defer removeTemporary(temporary)

	result, err := capture.Run(binary, append([]string{"linters", "--json"}, flags...), env, temporary)
	if err != nil {
		return false, failure("run golangci-lint linters", err)
	}

	output, err := os.ReadFile(temporary)
	if err != nil {
		return false, failure("read enabled linters", err)
	}

	if result.Status != 0 {
		return false, failure("read enabled linters", fmt.Errorf("%w %d: %s", errLinterSelection, result.Status, output))
	}

	var configured struct {
		Enabled []struct {
			Name string `json:"name"`
		} `json:"Enabled"`
	}
	err = json.Unmarshal(output, &configured)
	if err != nil {
		return false, failure("decode enabled linters", err)
	}

	for _, linter := range configured.Enabled {
		if linter.Name == "wsl_v5" {
			return true, nil
		}
	}

	return false, nil
}

func captureWhitespace(binary string, args, env []string, outputPath string) error {
	config, remaining, _, err := whitespaceSettings(args, false)
	if err != nil {
		return err
	}
	defer removeTemporary(config)

	options := []string{golangciRunCommand, "--enable-only=wsl_v5", configFlag, config, "--issues-exit-code=0"}
	args = make([]string, 0, len(options)+len(remaining))
	args = append(args, options...)
	args = append(args, remaining...)
	temporary, err := temporaryFile("", "go-mk-whitespace-*.out", nil)
	if err != nil {
		return err
	}
	defer removeTemporary(temporary)

	result, err := capture.Run(binary, args, env, temporary)
	if err != nil {
		return failure("capture whitespace findings", err)
	}

	if result.Status != 0 {
		contents, err := os.ReadFile(temporary)
		if err != nil {
			return failure("read whitespace capture error", err)
		}

		return failure("capture whitespace findings", fmt.Errorf("%w %d: %s", errWhitespaceStatus, result.Status, contents))
	}

	return appendCapture(outputPath, temporary)
}

func appendCapture(destination, source string) error {
	input, err := os.Open(source)
	if err != nil {
		return failure("open whitespace findings", err)
	}

	defer func() { _ = input.Close() }()

	output, err := os.OpenFile(destination, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return failure("open combined lint output", err)
	}

	_, copyErr := io.Copy(output, input)

	closeErr := output.Close()
	err = errors.Join(copyErr, closeErr)
	if err != nil {
		return failure("append whitespace findings", err)
	}

	return nil
}
