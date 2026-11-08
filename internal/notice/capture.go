package notice

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
)

var (
	errRelativeEngine        = errors.New("historical capture requires an absolute go-mk executable path")
	errConfigPathMissing     = errors.New("requires a configuration path")
	errConfigOutsideCheckout = errors.New("historical capture requires a golangci-lint config inside")
	errConfigWhitespace      = errors.New("historical config path contains whitespace unsupported by lint flags")
)

// CaptureStaticcheck limits a notice capture to its selected baseline scope.
//
// The function combines current-platform and other-platform findings before
// applying STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN.
//
// The callbacks perform these operations:
//   - capture writes the current-platform findings.
//   - otherPlatforms adds findings for the remaining target platforms.
//   - rootPath supplies the root for normalizing diagnostic paths.
func CaptureStaticcheck[Mode ~int](
	rawPath string,
	findingsPath string,
	capture func(string, string, Mode) error,
	mode Mode,
	otherPlatforms func(string) error,
	rootPath func() string,
) error {
	if err := capture(rawPath, findingsPath, mode); err != nil {
		return err
	}

	if err := otherPlatforms(findingsPath); err != nil {
		return err
	}

	pattern, err := regexp.Compile(os.Getenv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN"))
	if err != nil {
		return failure("compile staticcheck notice scope", err)
	}

	lines, err := readHistoryFindings(findingsPath)
	if err != nil {
		return err
	}

	root := rootPath()
	scoped := make([]string, 0, len(lines))

	for _, line := range lines {
		payload, found := findings.Baseline(line, "staticcheck-extra", root, root)
		if found && pattern.MatchString(payload) {
			scoped = append(scoped, payload)
		}
	}

	slices.Sort(scoped)

	return writeHistoryFindings(findingsPath, slices.Compact(scoped))
}

// CaptureInDirectory prepares and analyzes a historical module.
//
// The current go-mk executable prepares the historical module before capture.
// The function restores the caller's directory and environment after
// capture returns.
//
// Historical capture performs these steps:
//   - Prepare the workspace and cgo dependencies.
//   - Run capture with a separate lint cache and staged configuration.
//   - Restore the caller's working directory and environment.
func CaptureInDirectory(
	directory string,
	rawPath string,
	findingsPath string,
	capture func(string, string) error,
) error {
	engine, err := os.Executable()
	if err != nil {
		return failure("locate current go-mk engine", err)
	}

	return CaptureWithEngine(engine, directory, rawPath, findingsPath, capture)
}

// CaptureWithEngine prepares a historical module with a go-mk executable.
//
// The engine path must be absolute. The callback runs in the historical module
// after workspace and cgo preparation, with a separate golangci-lint cache.
//
// The caller supplies these inputs:
//   - engine selects the current go-mk executable.
//   - directory selects the historical module.
//   - capture writes the raw output and normalized findings.
func CaptureWithEngine(
	engine string,
	directory string,
	rawPath string,
	findingsPath string,
	capture func(string, string) error,
) error {
	var restoreError error
	captureError := captureWithEnvironment(engine, directory, rawPath, findingsPath, capture, &restoreError)
	combinedError := errors.Join(captureError, restoreError)
	if combinedError != nil {
		slog.Error("notice historical capture failed", slog.Any("err", combinedError))
	}

	return combinedError
}

func captureWithEnvironment(
	engine string,
	directory string,
	rawPath string,
	findingsPath string,
	capture func(string, string) error,
	restoreError *error,
) error {
	if !filepath.IsAbs(engine) {
		return inputFailure(errRelativeEngine, "", ": "+strconv.Quote(engine))
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return failure("read notice working directory", err)
	}

	module, err := filepath.Abs(directory)
	if err != nil {
		return failure("resolve historical module directory", err)
	}

	environment, err := historicalEnvironment(module)
	if err != nil {
		return err
	}

	for name, value := range environment {
		previous, present := os.LookupEnv(name)
		if err := os.Setenv(name, value); err != nil {
			return failure("set "+name+" for historical capture", err)
		}

		defer restoreVariable(name, previous, present, restoreError)
	}

	if err := os.Chdir(module); err != nil {
		return failure("enter historical module", err)
	}

	defer func() {
		if err := os.Chdir(workingDir); err != nil {
			*restoreError = errors.Join(*restoreError, failure("restore notice working directory", err))
		}
	}()

	if err := prepareHistory(engine); err != nil {
		return err
	}

	return capture(rawPath, findingsPath)
}

func historicalEnvironment(module string) (map[string]string, error) {
	// golangci-lint caches absolute source paths, including those in its findings.
	cache, err := os.MkdirTemp(filepath.Join(module, ".make"), "notice-golangci-cache-")
	if err != nil {
		return nil, failure("create historical golangci-lint cache", err)
	}

	slog.Info("notice prepare capture environment", slog.String("cache", cache))
	environment := map[string]string{"_GO_MK_ROOT": module, "GOLANGCI_LINT_CACHE": cache}

	for _, name := range []string{"GOLANGCI_LINT_FLAGS", "GOLANGCI_LINT_RUN_FLAGS"} {
		flags, err := stageConfigFlags(module, os.Getenv(name))
		if err != nil {
			return nil, failure("stage "+name+" for historical capture", err)
		}

		if flags != "" {
			environment[name] = flags
		}
	}

	for _, name := range []string{"STATICCHECK_EXTRA_BIN", "GOLANGCI_LINT"} {
		value := os.Getenv(name)
		if value == "" {
			continue
		}

		resolved, err := exec.LookPath(value)
		if err != nil {
			return nil, failure("locate "+name+" for historical capture", err)
		}

		absolute, err := filepath.Abs(resolved)
		if err != nil {
			return nil, failure("resolve "+name+" for historical capture", err)
		}

		environment[name] = absolute
	}

	return environment, nil
}

func stageConfigFlags(module, value string) (string, error) {
	flags := strings.Fields(value)
	for index := 0; index < len(flags); index++ {
		flag := flags[index]
		var path string
		var prefix string

		switch {
		case flag == "--":
			return strings.Join(flags, " "), nil
		case flag == "-c" || flag == "--config":
			index++
			if index == len(flags) || strings.HasPrefix(flags[index], "-") {
				return "", inputFailure(errConfigPathMissing, flag+" ", "")
			}

			path = flags[index]
		case strings.HasPrefix(flag, "--config="), strings.HasPrefix(flag, "-c="):
			option, argument, _ := strings.Cut(flag, "=")
			prefix, path = option+"=", argument
		case strings.HasPrefix(flag, "-c"):
			prefix, path = "-c", strings.TrimPrefix(flag, "-c")
		default:
			continue
		}

		if path == "" {
			return "", inputFailure(errConfigPathMissing, flag+" ", "")
		}

		staged, err := stageConfig(module, path)
		if err != nil {
			return "", err
		}

		flags[index] = prefix + staged
	}

	return strings.Join(flags, " "), nil
}

func stageConfig(module, path string) (string, error) {
	current, err := checkoutRoot(".")
	if err != nil {
		return "", err
	}

	historical, err := checkoutRoot(module)
	if err != nil {
		return "", err
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", failure("resolve config path "+path, err)
	}

	directory, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", failure("resolve config directory for "+path, err)
	}

	absolute = filepath.Join(directory, filepath.Base(absolute))
	relative, err := filepath.Rel(current, absolute)
	if err != nil {
		return "", failure("resolve config path inside current checkout", err)
	}

	if !filepath.IsLocal(relative) {
		return "", inputFailure(errConfigOutsideCheckout, "", " "+current+": "+absolute)
	}

	checkout, err := os.OpenRoot(current)
	if err != nil {
		return "", failure("open current checkout for config staging", err)
	}

	defer func() { _ = checkout.Close() }()

	contents, err := checkout.ReadFile(relative)
	if err != nil {
		return "", failure("read current golangci-lint config "+absolute, err)
	}

	return writeStagedConfig(historical, relative, contents)
}

func checkoutRoot(directory string) (string, error) {
	slog.Info("resolve notice checkout", slog.String("directory", directory))
	command := exec.CommandContext(context.Background(), "git", "-C", directory, "rev-parse", "--show-toplevel")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", failure("resolve Git checkout for "+directory+": "+strings.TrimSpace(string(output)), err)
	}

	return strings.TrimSpace(string(output)), nil
}

func writeStagedConfig(historical, relative string, contents []byte) (string, error) {
	checkout, err := os.OpenRoot(historical)
	if err != nil {
		return "", failure("open historical checkout", err)
	}

	defer func() { _ = checkout.Close() }()

	directory := filepath.Dir(relative)
	if err := checkout.MkdirAll(directory, 0o755); err != nil {
		return "", failure("create historical config directory", err)
	}

	// Preserve the config directory for paths relative to the config file.
	name := filepath.Join(directory, ".notice-"+rand.Text()+filepath.Ext(relative))
	path := filepath.Join(historical, name)
	if len(strings.Fields(path)) != 1 {
		return "", inputFailure(errConfigWhitespace, "", ": "+path)
	}

	if err := checkout.WriteFile(name, contents, 0o600); err != nil {
		return "", failure("stage current golangci-lint config", err)
	}

	return path, nil
}

func restoreVariable(name, previous string, present bool, captureErr *error) {
	var err error
	if present {
		err = os.Setenv(name, previous)
	} else {
		err = os.Unsetenv(name)
	}

	if err != nil {
		*captureErr = errors.Join(*captureErr, failure("restore "+name+" after historical capture", err))
	}
}

func failure(message string, err error) error {
	wrapped := fmt.Errorf("%s: %w", message, err)
	slog.Error("notice operation failed", slog.String("operation", message), slog.Any("err", wrapped))

	return wrapped
}
