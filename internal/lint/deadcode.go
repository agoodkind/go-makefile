package lint

import (
	"os"
	"path/filepath"
)

const (
	deadcodeCheck                 = "deadcode"
	deadcodeInstallEnv            = "DEADCODE_INSTALL"
	deadcodeDefaultInstall        = "golang.org/x/tools/cmd/deadcode@latest"
	deadcodeRawFile               = "deadcode.raw.out"
	deadcodeFindingsFile          = "deadcode.out"
	deadcodeDefaultExcludeEnv     = "DEADCODE_DEFAULT_EXCLUDE_PATHS"
	deadcodeDefaultExcludePattern = `_test\.go:`
	deadcodeExcludeEnv            = "DEADCODE_EXCLUDE_PATHS"
	deadcodeBaselineEnv           = "DEADCODE_BASELINE"
	deadcodeBaselineFile          = ".deadcode-baseline.txt"
)

// CheckDeadcode runs the analyzer and returns the baseline gate verdict.
//
// Preparation and capture errors stop the check before it evaluates the baseline.
//
//	status := CheckDeadcode(dir, prepared, ensure, env, install, capture, read, evaluate, errorStatus)
func CheckDeadcode(
	directory string,
	toolsPrepared bool,
	ensure func() error,
	envDefault func(string, string) string,
	install func(string) error,
	capture func(string, string) error,
	readLines func(string) ([]string, error),
	evaluate func(string, []string, string, string, string, string) (bool, error),
	errorStatus func(error) int,
) int {
	if err := ensure(); err != nil {
		return errorStatus(err)
	}

	if !toolsPrepared {
		if err := install(envDefault(deadcodeInstallEnv, deadcodeDefaultInstall)); err != nil {
			return errorStatus(err)
		}
	}

	rawPath := filepath.Join(directory, deadcodeRawFile)
	findingsPath := filepath.Join(directory, deadcodeFindingsFile)
	if err := capture(rawPath, findingsPath); err != nil {
		return errorStatus(err)
	}

	current, err := readLines(findingsPath)
	if err != nil {
		return errorStatus(err)
	}

	excludePattern := ExcludePattern(
		envDefault(deadcodeDefaultExcludeEnv, deadcodeDefaultExcludePattern),
		os.Getenv(deadcodeExcludeEnv),
	)
	passed, err := evaluate(
		deadcodeCheck,
		current,
		envDefault(deadcodeBaselineEnv, deadcodeBaselineFile),
		"The deadcode lint gate found unreachable code. Remove the reported code.",
		excludePattern,
		"",
	)
	if err != nil {
		return errorStatus(err)
	}

	if !passed {
		return 1
	}

	return 0
}
