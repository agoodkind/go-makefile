package execution

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/lint"
	"goodkind.io/go-makefile/internal/notice/history"
)

func (runner *Runner) planGolangci(
	ctx context.Context,
	gate string,
	rule string,
	introduced time.Time,
	exclude string,
) (baseline.Component, int) {
	var component baseline.Component
	if code := validMode(); code != 0 {
		return component, code
	}

	pattern := lint.GolangciScopePattern(
		os.Getenv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN"),
		os.Getenv("RULE"),
		os.Getenv("LINTER"),
	)
	if pattern == "" {
		stdout(
			"auto-baseline: missing scope; set LINTER, RULE, or GOLANGCI_LINT_BASELINE_SCOPE_PATTERN\n",
		)

		return component, 1
	}

	if err := os.MkdirAll(makeDirectory, 0o755); err != nil {
		return component, failureStatus(fileFailure("create baseline capture directory", err))
	}

	if err := runner.tools(); err != nil {
		return component, failureStatus(err)
	}

	findingsPath := filepath.Join(makeDirectory, "golangci-lint-scope-baseline.out")
	rawPath := filepath.Join(makeDirectory, "golangci-lint-scope-baseline.raw.out")
	if err := ctx.Err(); err != nil {
		return component, failureStatus(err)
	}

	if err := runner.golangci(rawPath, findingsPath); err != nil {
		return component, failureStatus(err)
	}

	if err := history.RestrictFindings(
		ctx,
		findingsPath,
		introduced,
		gate,
		rule,
		runner.git,
		runner.golangci,
	); err != nil {
		return component, failureStatus(err)
	}

	component = baseline.Component{
		Title:          "golangci-lint",
		Label:          "golangci-lint",
		BaselineFile:   envDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt"),
		FindingsFile:   findingsPath,
		Mode:           "sync",
		ExcludePattern: exclude,
		ScopePattern:   pattern,
	}

	return component, 0
}

func (runner *Runner) planStaticcheck(
	ctx context.Context,
	gate string,
	rule string,
	introduced time.Time,
	exclude string,
) (baseline.Component, int) {
	var component baseline.Component
	if code := validMode(); code != 0 {
		return component, code
	}

	pattern := os.Getenv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN")
	if pattern == "" {
		stdout("auto-baseline: missing scope; set STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN\n")

		return component, 1
	}

	if err := os.MkdirAll(makeDirectory, 0o755); err != nil {
		return component, failureStatus(fileFailure("create baseline capture directory", err))
	}

	if code := runner.analyzer(); code != 0 {
		return component, code
	}

	findingsPath := filepath.Join(makeDirectory, "staticcheck-extra-scope-baseline.out")
	rawPath := filepath.Join(makeDirectory, "staticcheck-extra-scope-baseline.raw.out")
	if err := ctx.Err(); err != nil {
		return component, failureStatus(err)
	}

	if err := runner.staticcheck(rawPath, findingsPath); err != nil {
		return component, failureStatus(err)
	}

	if err := history.RestrictFindings(
		ctx,
		findingsPath,
		introduced,
		gate,
		rule,
		runner.git,
		runner.staticcheck,
	); err != nil {
		return component, failureStatus(err)
	}

	component = baseline.Component{
		Title:          "staticcheck-extra",
		Label:          "staticcheck-extra",
		BaselineFile:   envDefault("STATICCHECK_EXTRA_BASELINE", ".staticcheck-extra-baseline.txt"),
		FindingsFile:   findingsPath,
		Mode:           "sync",
		ExcludePattern: exclude,
		ScopePattern:   pattern,
	}

	return component, 0
}

func validMode() int {
	mode := envDefault("BASELINE_UPDATE_MODE", "sync")
	if _, err := baseline.ParseMode(mode); err != nil {
		stdout("unknown baseline update mode: " + mode + "\n")

		return invalidModeStatus
	}

	return 0
}

const invalidModeStatus = 2
