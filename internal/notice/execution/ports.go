// Package execution runs notice workflows through existing tool and file IO.
package execution

import (
	"context"
	"time"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/notice/history"
)

// Runner connects notice decisions to the engine's execution operations.
//
// The operations acquire tools, capture findings, and write baseline files.
// The runner owns notice parsing, acknowledgment, and automatic scope planning.
//
// The command adapter creates a runner with New.
type Runner struct {
	git         history.GitOutput
	tools       func() error
	analyzer    func() int
	golangci    history.Capture
	staticcheck history.Capture
	write       func(baseline.Manifest, bool) error
}

type settings struct {
	gate               string
	baselines          []string
	ci                 bool
	golangciExclude    string
	staticcheckExclude string
}

// New connects the existing execution operations to notice processing.
//
// Each operation retains its tool, capture, or file-write responsibility.
// The constructor does not execute an operation.
//
// Invoke the default command boundary after construction:
//
//	runner.RunDefault("", baselines, ci, golangciExclude, staticcheckExclude)
func New(
	git history.GitOutput,
	tools func() error,
	analyzer func() int,
	golangci history.Capture,
	staticcheck history.Capture,
	write func(baseline.Manifest, bool) error,
) *Runner {
	return &Runner{
		git:         git,
		tools:       tools,
		analyzer:    analyzer,
		golangci:    golangci,
		staticcheck: staticcheck,
		write:       write,
	}
}

// RunDefault executes the notice command with its default entry context.
//
// A gate selects only matching automatic notices. A full pass also announces
// changes and records the highest notice number.
//
// A full notice pass uses:
//
//	runner.RunDefault("", baselines, ci, golangciExclude, staticcheckExclude)
func (runner *Runner) RunDefault(
	gate string,
	baselines []string,
	ciRun bool,
	golangciExclude string,
	staticcheckExclude string,
) int {
	return runner.run(
		context.Background(),
		settings{
			gate:               gate,
			baselines:          baselines,
			ci:                 ciRun,
			golangciExclude:    golangciExclude,
			staticcheckExclude: staticcheckExclude,
		},
	)
}

// PlanGolangciDefault captures one automatic Golangci scope.
//
// The returned component contains only findings eligible before introduced.
// A nonzero status leaves the caller's collector unchanged.
//
// Inspect the result before adding it to the collector:
//
//	component, status := runner.PlanGolangciDefault(gate, rule, introduced, exclude)
func (runner *Runner) PlanGolangciDefault(
	gate string,
	rule string,
	introduced time.Time,
	exclude string,
) (baseline.Component, int) {
	return runner.planGolangci(context.Background(), gate, rule, introduced, exclude)
}

// PlanStaticcheckDefault captures one automatic staticcheck scope.
//
// The returned component contains only findings eligible before introduced.
// A nonzero status leaves the caller's collector unchanged.
//
// Inspect the result before adding it to the collector:
//
//	component, status := runner.PlanStaticcheckDefault(gate, rule, introduced, exclude)
func (runner *Runner) PlanStaticcheckDefault(
	gate string,
	rule string,
	introduced time.Time,
	exclude string,
) (baseline.Component, int) {
	return runner.planStaticcheck(context.Background(), gate, rule, introduced, exclude)
}
