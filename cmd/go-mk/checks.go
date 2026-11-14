package main

import (
	"errors"

	"goodkind.io/go-makefile/internal/checkrun"
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

// runOneGate runs one lint gate while collecting its verdict in-process and
// turns the recorded outcome into a StepResult under the gate-list name.
func runOneGate(gateName string, runner func() int) (report.StepResult, int) {
	collectedGateOutcome = gateOutcome{}
	status := runner()
	outcome := collectedGateOutcome

	return gateStep(gateName, outcome.marker, outcome.found, status, outcome.rest), status
}

// buildCheckChecks delegates the default check plan to the shared runner.
func buildCheckChecks() []checkrun.Check {
	return checkrun.Plan(
		vetChecks(),
		gateChecks(),
		runGoVersionStep,
		runCgoStubCheckStep,
		runPlatformStubCheckStep,
	)
}

// vetChecks delegates platform-specific vet planning to the shared runner.
func vetChecks() []checkrun.Check {
	return checkrun.Vet(platformMatrix(), runVetStep, platformStepCheck)
}

// gateChecks delegates lint planning to the shared runner.
func gateChecks() []checkrun.Check {
	return checkrun.Gates(
		gateRunners(),
		splitWords(lintEnvDefault("LINT_GATES", defaultLintGates)),
		platformMatrix(),
		runOneGate,
		unknownGateStep,
		platformGateCheck,
	)
}

// platformGateCheck delegates platform state changes to the shared runner.
func platformGateCheck(
	gateName string,
	runner func() int,
	target platformTarget,
) checkrun.Check {
	return checkrun.PlatformGate(
		gateName,
		runner,
		target,
		target.label(),
		&activePlatform,
		runOneGate,
	)
}

// platformStepCheck delegates platform state changes to the shared runner.
func platformStepCheck(
	stepName string,
	runner func() (report.StepResult, int),
	target platformTarget,
) checkrun.Check {
	return checkrun.PlatformStep(stepName, runner, target, target.label(), &activePlatform)
}

// prepareChecks installs every tool the run needs before the live display
// starts, so no go install or go build streams to the terminal mid-display. It
// ensures the make dir, clears the per-gate failure record, installs the lint
// tool trio, gocyclo, and deadcode, resolves the staticcheck-extra analyzer
// binary, and, when includeGovulncheck is set, installs govulncheck. On success
// it sets checksToolsPrepared so gate runners skip their own install.
func prepareChecks(includeGovulncheck bool) error {
	if err := ensureMakeDir(); err != nil {
		return err
	}

	clearFailedGateFile()
	if err := runLintTools(); err != nil {
		return err
	}

	if err := installGoTool(
		lintEnvDefault("GOCYCLO_INSTALL", "github.com/fzipp/gocyclo/cmd/gocyclo@latest"),
	); err != nil {
		return err
	}

	if err := installGoTool(
		lintEnvDefault("DEADCODE_INSTALL", "golang.org/x/tools/cmd/deadcode@latest"),
	); err != nil {
		return err
	}

	if status := runStaticcheckBin(); status != 0 {
		return errStaticcheckPrepare
	}

	if includeGovulncheck {
		if err := installGoTool(
			lintEnvDefault("GOVULNCHECK_INSTALL", defaultGovulncheckInstall),
		); err != nil {
			return err
		}
	}

	checksToolsPrepared = true

	return nil
}

// runChecks delegates progress reporting to the shared runner.
func runChecks(title string, checks []checkrun.Check) int {
	return checkrun.Run(title, checks, &gateCollecting, writeStdout)
}
