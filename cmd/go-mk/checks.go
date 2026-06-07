// Check orchestration for go-mk. A check is one named unit of the build-check or
// lint run (vet, a lint gate, govulncheck) that yields a report.StepResult. The
// orchestrator runs the checks in order and surfaces each result as it lands:
// a bubbletea progress display on a terminal, or streamed status rows when
// stdout is not a terminal. Both paths drive the same executeChecks runner and
// render with the internal/report helpers, so the streamed run and the batch
// report are byte-identical.
//
// Gates report their verdict in-process. While the run is collecting, the gate
// runners record a report.GateMarker into the package-level outcome instead of
// printing it to a stream, which is what frees the terminal for the progress
// display. The tool installs happen once up front (prepareChecks) so no
// go install or go build streams to the terminal mid-display.
package main

import (
	"errors"
	"os"

	"golang.org/x/term"

	"goodkind.io/go-makefile/internal/report"
)

// errStaticcheckPrepare reports that prepareChecks could not resolve the
// staticcheck-extra analyzer binary up front.
var errStaticcheckPrepare = errors.New("staticcheck-extra: could not resolve analyzer binary")

// gateCollecting reports whether the gate runners should record their verdict
// in-process instead of printing it. The orchestrator sets it for the duration
// of executeChecks; standalone gate commands and the GO_MK_LOG=debug path leave
// it false and keep the per-gate human render.
var gateCollecting bool

// checksToolsPrepared reports whether prepareChecks already installed every tool
// the run needs. The gate and govulncheck runners skip their own install when it
// is set, so the live display is not interrupted by a go install or go build.
var checksToolsPrepared bool

// gateOutcome carries one gate's recorded result back to the orchestrator. found
// marks a structured marker (the gate computed a verdict); rest holds the
// captured tool-failure lines for a gate that exited before producing a verdict.
type gateOutcome struct {
	marker report.GateMarker
	rest   []string
	found  bool
}

// collectedGateOutcome holds the most recent gate's recorded outcome. The
// orchestrator resets it before each gate and reads it after, so a gate that
// records a verdict and then a tool failure keeps the terminal failure.
var collectedGateOutcome gateOutcome

// recordGateMarker stores a gate's structured verdict when the run is
// collecting. It replaces the marker line the gate used to print for the parent
// to scrape.
func recordGateMarker(marker report.GateMarker) {
	if gateCollecting {
		collectedGateOutcome = gateOutcome{marker: marker, found: true}
	}
}

// recordGateToolFailure stores the captured lines for a gate that failed before
// producing a verdict, so the orchestrator can show them under the failed step.
func recordGateToolFailure(rest []string) {
	if gateCollecting {
		collectedGateOutcome = gateOutcome{rest: rest, found: false}
	}
}

// check is one named unit of a run. run executes the unit and returns its
// resolved result and exit status; a non-zero status makes the whole run fail.
type check struct {
	name string
	run  func() (report.StepResult, int)
}

// runOneGate runs one lint gate while collecting its verdict in-process and
// turns the recorded outcome into a StepResult under the gate-list name.
func runOneGate(gateName string, runner func() int) (report.StepResult, int) {
	collectedGateOutcome = gateOutcome{}
	status := runner()
	outcome := collectedGateOutcome
	return gateStep(gateName, outcome.marker, outcome.found, status, outcome.rest), status
}

// buildCheckChecks lists the build-check units in order: vet, every gate in
// LINT_GATES, then govulncheck.
func buildCheckChecks() []check {
	checks := []check{{name: "vet", run: runVetStep}}
	checks = append(checks, gateChecks()...)
	checks = append(checks, check{name: "govulncheck", run: runGovulncheckStep})
	return checks
}

// gateChecks lists the lint gates in LINT_GATES order. Each closure captures its
// gate name and runner so the orchestrator runs them one at a time.
func gateChecks() []check {
	runners := gateRunners()
	gateList := splitWords(lintEnvDefault("LINT_GATES", defaultLintGates))
	checks := make([]check, 0, len(gateList))
	for _, gateName := range gateList {
		name := gateName
		runner, ok := runners[name]
		if !ok {
			checks = append(checks, check{name: name, run: func() (report.StepResult, int) {
				return unknownGateStep(name), 1
			}})
			continue
		}
		checks = append(checks, check{name: name, run: func() (report.StepResult, int) {
			return runOneGate(name, runner)
		}})
	}
	return checks
}

// checkNameWidth returns the status-table column width for the given checks, so
// the first streamed row aligns with the last.
func checkNameWidth(checks []check) int {
	steps := make([]report.StepResult, len(checks))
	for index, current := range checks {
		steps[index] = report.StepResult{Name: current.name}
	}
	return report.NameWidth(steps)
}

// executeChecks runs each check in order, calling onDone with its result as it
// completes, and returns the aggregate exit status (non-zero when any check
// failed). onDone must not be nil.
func executeChecks(checks []check, onDone func(int, report.StepResult)) int {
	status := 0
	for index, current := range checks {
		result, code := current.run()
		if code != 0 {
			status = code
		}
		onDone(index, result)
	}
	return status
}

// prepareChecks installs every tool the run needs before the live display
// starts, so no go install or go build streams to the terminal mid-display. It
// ensures the make dir, clears the per-gate failure record, installs the lint
// tool trio, gocyclo, and deadcode, resolves the staticcheck-extra analyzer
// binary, and, when includeGovulncheck is set, installs govulncheck. On success
// it sets checksToolsPrepared so the gate and govulncheck runners skip their own
// install.
func prepareChecks(includeGovulncheck bool) error {
	if err := ensureMakeDir(); err != nil {
		return err
	}
	clearFailedGateFile()
	if err := runLintTools(); err != nil {
		return err
	}
	if err := installGoTool(lintEnvDefault("GOCYCLO_INSTALL", "github.com/fzipp/gocyclo/cmd/gocyclo@latest")); err != nil {
		return err
	}
	if err := installGoTool(lintEnvDefault("DEADCODE_INSTALL", "golang.org/x/tools/cmd/deadcode@latest")); err != nil {
		return err
	}
	if status := runStaticcheckBin(); status != 0 {
		return errStaticcheckPrepare
	}
	if includeGovulncheck {
		if err := installGoTool("golang.org/x/vuln/cmd/govulncheck@latest"); err != nil {
			return err
		}
	}
	checksToolsPrepared = true
	return nil
}

// runChecks renders a titled run of checks, choosing the bubbletea progress
// display on a terminal and streamed status rows otherwise. It sets the
// in-process gate collection for the duration of the run.
func runChecks(title string, checks []check) int {
	gateCollecting = true
	defer func() { gateCollecting = false }()
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return runChecksTUI(title, checks)
	}
	return runChecksStream(title, checks)
}

// runChecksStream prints the title, then each status row the instant its check
// completes, then the per-failure findings blocks and the verdict footer. The
// full output is byte-identical to report.Render.
func runChecksStream(title string, checks []check) int {
	writeStdout(title + "\n\n")
	width := checkNameWidth(checks)
	results := make([]report.StepResult, len(checks))
	status := executeChecks(checks, func(index int, result report.StepResult) {
		results[index] = result
		writeStdout(report.StepRow(width, result) + "\n")
	})
	for _, result := range results {
		writeStdout(report.FindingsBlock(result))
	}
	writeStdout(report.Footer(failedStepNames(results)))
	return status
}

// failedStepNames returns the names of the failed steps in order, for the
// verdict footer.
func failedStepNames(results []report.StepResult) []string {
	failed := make([]string, 0, len(results))
	for _, result := range results {
		if result.Status == report.StatusFailed {
			failed = append(failed, result.Name)
		}
	}
	return failed
}
