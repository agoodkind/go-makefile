package execution

import (
	"context"
	"os"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/notice/history"
)

func (runner *Runner) apply(
	ctx context.Context,
	options settings,
	current record,
	value directive,
	appliedFile string,
	applied map[string]bool,
) {
	if value.gate != "golangci" && value.gate != staticcheckGate {
		stderr(
			"go-makefile notice #" + current.id + ": unsupported auto-baseline gate '" + value.gate + "'; skipping\n",
		)

		return
	}

	stderr(
		"go-makefile notice #" + current.id + ": auto-baselining existing findings for " + current.directive + "\n",
	)
	code := runner.applyScope(ctx, options, value)
	if code != 0 {
		reportCaptureFailure(current.id, value.gate)

		return
	}

	if err := appendApplied(appliedFile, current.id); err != nil {
		reportAppliedFailure(current.id, value.gate, appliedFile)

		return
	}

	applied[current.id] = true
	baselinePath := envDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt")
	if value.gate == staticcheckGate {
		baselinePath = envDefault("STATICCHECK_EXTRA_BASELINE", ".staticcheck-extra-baseline.txt")
	}

	stderr(
		"go-makefile notice #" + current.id + ": wrote " + baselinePath +
			". Review with 'git diff " + baselinePath + "' and commit it together with " + appliedFile + ".\n",
	)
}

func (runner *Runner) applyScope(ctx context.Context, options settings, value directive) int {
	if value.policy == history.Upgrade {
		if err := runner.format(); err != nil {
			return failureStatus(err)
		}
	}

	if value.gate == staticcheckGate {
		previous, had := os.LookupEnv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN")
		_ = os.Setenv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN", value.pattern)
		defer restoreEnv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN", previous, had)
		component, code := runner.planStaticcheck(
			ctx,
			value.gate,
			value.rule,
			value.introduced,
			options.staticcheckExclude,
			value.policy,
		)
		if code != 0 {
			return code
		}

		return runner.flush(component)
	}

	previousLinter, hadLinter := os.LookupEnv("LINTER")
	previousRule, hadRule := os.LookupEnv("RULE")
	previousPattern, hadPattern := os.LookupEnv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN")
	_ = os.Setenv("LINTER", value.linter)
	_ = os.Setenv("RULE", value.rule)
	_ = os.Setenv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN", value.pattern)
	defer func() {
		restoreEnv("LINTER", previousLinter, hadLinter)
		restoreEnv("RULE", previousRule, hadRule)
		restoreEnv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN", previousPattern, hadPattern)
	}()
	component, code := runner.planGolangci(
		ctx,
		value.gate,
		value.rule,
		value.introduced,
		options.golangciExclude,
		value.policy,
	)
	if code != 0 {
		return code
	}

	return runner.flush(component)
}

func (runner *Runner) flush(component baseline.Component) int {
	return failureStatus(
		runner.write(
			baseline.Manifest{
				Now:        "",
				Components: []baseline.Component{component},
			},
			false,
		),
	)
}

func reportCaptureFailure(noticeID, gate string) {
	if gate == staticcheckGate {
		stderr(
			"go-makefile notice #" + noticeID + ": auto-baseline failed; it runs again on the next build\n",
		)

		return
	}

	stderr(
		"go-makefile notice #" + noticeID + ": auto-baseline failed; run it manually with the scoped baseline target\n",
	)
}

func reportAppliedFailure(noticeID, gate, path string) {
	if gate == staticcheckGate {
		stderr(
			"go-makefile notice #" + noticeID + ": could not record applied notice in " + path +
				"; the auto-baseline runs again on the next build\n",
		)

		return
	}

	reportCaptureFailure(noticeID, gate)
}
