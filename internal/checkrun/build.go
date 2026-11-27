package checkrun

import (
	"fmt"
	"os"
	"strings"

	"goodkind.io/go-makefile/internal/logsummary"
)

type buildShard string
type shardCheckName string

const (
	vetShard       buildShard     = "vet"
	golangciShard  buildShard     = "golangci"
	formatShard    buildShard     = "format"
	remainingShard buildShard     = "remaining"
	vetCheckName   shardCheckName = "vet"
	lintCheckName  shardCheckName = "lint-golangci"
	fmtCheckName   shardCheckName = "lint-format"
)

const (
	checkLogEnv     = "GO_MK_LOG"
	buildCheckTitle = "go-mk build-check"
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
	notice func(string) int,
	prepare func(bool) error,
	statusFromError func(error) int,
	execute func(string, []Check) int,
	plan func() []Check,
	debug func() int,
) int {
	shard := buildShard(os.Getenv("GO_MK_BUILD_CHECK_SHARD"))
	if shard != "" {
		checks, err := selectBuildShard(shard, plan())
		if err != nil {
			return statusFromError(err)
		}
		if shard == golangciShard {
			notice(string(shard))
		}

		return execute(buildCheckTitle+" "+string(shard), checks)
	}
	notice("")
	if logsummary.ParseMode(os.Getenv(checkLogEnv)) == logsummary.ModeDebug {
		return debug()
	}

	if err := prepare(false); err != nil {
		return statusFromError(err)
	}

	return execute(buildCheckTitle, plan())
}

func selectBuildShard(shard buildShard, plan []Check) ([]Check, error) {
	var checks []Check
	for _, check := range plan {
		if shardForCheck(check.Name) == shard {
			checks = append(checks, check)
		}
	}
	if len(checks) == 0 && shard != golangciShard && shard != formatShard {
		return nil, fmt.Errorf("unknown or empty build-check shard %q", shard)
	}

	return checks, nil
}

func shardForCheck(name string) buildShard {
	base, _, _ := strings.Cut(name, " (")
	switch shardCheckName(base) {
	case vetCheckName:
		return vetShard
	case lintCheckName:
		return golangciShard
	case fmtCheckName:
		return formatShard
	default:
		return remainingShard
	}
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
