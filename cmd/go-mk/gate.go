// Token gate for go-mk, ported from scripts/go-mk-gate.sh. The gate writes a
// stamp file only when the confirm value is affirmative and the slugified token
// matches the slugified output of the token command. This file lives in package
// main and owns flag parsing, running the token command, and writing the stamp;
// the pure confirm and token-compare logic lives in internal/gate.
package main

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"goodkind.io/go-makefile/internal/gate"
)

// gateOptions holds the parsed go-mk gate flags, mirroring the go-mk-gate.sh
// argument surface: --stamp, --confirm-value, --token-value, --token-command.
type tokenGateOptions struct {
	stamp        string
	confirmValue string
	tokenValue   string
	tokenCommand string
}

// runGateConfirm is the gate subcommand, mirroring scripts/go-mk-gate.sh. It
// removes any existing stamp, returns success without a stamp when the confirm
// value is not affirmative or the token command fails or the tokens differ, and
// writes the stamp only when the confirm value is affirmative and the tokens
// match. It returns the process exit code.
func runGateConfirm(args []string) int {
	options, err := parseTokenGateOptions(args)
	if err != nil {
		writeStdout(err.Error() + "\n")
		return 2
	}
	if options.stamp == "" || options.tokenCommand == "" {
		writeStdout("go-mk gate: --stamp and --token-command are required\n")
		return 2
	}
	if err := removeStamp(options.stamp); err != nil {
		return statusFromError(err)
	}
	if !gate.ConfirmAccepted(options.confirmValue) {
		return 0
	}
	expectedRaw, ok := runTokenCommand(options.tokenCommand)
	if !ok {
		return 0
	}
	if !gate.TokensMatch(expectedRaw, options.tokenValue) {
		return 0
	}
	if err := writeStamp(options.stamp); err != nil {
		return statusFromError(err)
	}
	return 0
}

// parseGateOptions parses the gate flags, each taking the following argument as
// its value, mirroring the go-mk-gate.sh while-case loop.
func parseTokenGateOptions(args []string) (tokenGateOptions, error) {
	options := tokenGateOptions{}
	targets := map[string]*string{
		"--stamp":         &options.stamp,
		"--confirm-value": &options.confirmValue,
		"--token-value":   &options.tokenValue,
		"--token-command": &options.tokenCommand,
	}
	for index := 0; index < len(args); index++ {
		target, ok := targets[args[index]]
		if !ok {
			return tokenGateOptions{}, &unknownOptionError{option: args[index]}
		}
		if index+1 >= len(args) {
			return tokenGateOptions{}, &missingValueError{option: args[index]}
		}
		index++
		*target = args[index]
	}
	return options, nil
}

// removeStamp deletes the stamp file, tolerating a missing file, mirroring the
// go-mk-gate.sh `rm -f`. It mutates the filesystem, so it emits a boundary log.
func removeStamp(path string) error {
	slog.Info("gate remove stamp", slog.String("path", path))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// runTokenCommand runs the token command through sh -c, mirroring the
// go-mk-gate.sh `eval`. It returns the command stdout and true on success, or
// false when the command exits non-zero so the gate stays closed. It runs a
// process, so it emits a boundary log.
func runTokenCommand(command string) (string, bool) {
	slog.Info("gate run token command")
	out, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// writeStamp creates the stamp file and its parent directory, mirroring the
// go-mk-gate.sh `mkdir -p` then `: > stamp`. It mutates the filesystem, so it
// emits a boundary log.
func writeStamp(path string) error {
	slog.Info("gate write stamp", slog.String("path", path))
	if directory := filepath.Dir(path); directory != "" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, nil, 0o644)
}
