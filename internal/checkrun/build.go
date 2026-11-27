package checkrun

import (
	"os"

	"goodkind.io/go-makefile/internal/logsummary"
)

// Build runs default quality checks without vulnerability scanning.
//
// It prepares tools before reporting results.
// GO_MK_LOG selects the debug runner.
//
// Example:
//
//	status := Build(notice, prepare, statusFromError, execute, plan, debug)
func Build(
	notice func() int,
	prepare func(bool) error,
	statusFromError func(error) int,
	execute func(string, []Check) int,
	plan func() []Check,
	debug func() int,
) int {
	notice()
	if logsummary.ParseMode(os.Getenv("GO_MK_LOG")) == logsummary.ModeDebug {
		return debug()
	}

	if err := prepare(false); err != nil {
		return statusFromError(err)
	}

	return execute("go-mk build-check", plan())
}

// Raw runs the default checks with each tool's normal output.
//
// It returns a nonzero exit status when a check fails.
//
// Example:
//
//	status := Raw(acrossPlatforms, vet, statusFromError, lint, version)
func Raw(
	acrossPlatforms func(func() int) int,
	vet func() error,
	statusFromError func(error) int,
	lint func() int,
	version func() int,
) int {
	status := 0
	if code := acrossPlatforms(func() int { return statusFromError(vet()) }); code != 0 {
		status = code
	}

	if code := lint(); code != 0 {
		status = code
	}

	if code := version(); code != 0 {
		status = code
	}

	return status
}
