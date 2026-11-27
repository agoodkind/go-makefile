package lint

import (
	"os"
	"path/filepath"
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
		if err := install(
			envDefault("DEADCODE_INSTALL", "golang.org/x/tools/cmd/deadcode@latest"),
		); err != nil {
			return errorStatus(err)
		}
	}

	rawPath := filepath.Join(directory, "deadcode.raw.out")
	findingsPath := filepath.Join(directory, "deadcode.out")
	if err := capture(rawPath, findingsPath); err != nil {
		return errorStatus(err)
	}

	current, err := readLines(findingsPath)
	if err != nil {
		return errorStatus(err)
	}

	excludePattern := ExcludePattern(
		envDefault("DEADCODE_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("DEADCODE_EXCLUDE_PATHS"),
	)
	passed, err := evaluate(
		"deadcode",
		current,
		envDefault("DEADCODE_BASELINE", ".deadcode-baseline.txt"),
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
