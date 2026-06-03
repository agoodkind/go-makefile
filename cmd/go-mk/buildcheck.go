// build-check orchestration for go-mk. It runs vet, every lint gate, and
// govulncheck in one process, collects a StepResult for each, and prints the
// single run report. This replaces the make-level fan-out across vet, lint, and
// govulncheck so a full build emits one clean report instead of one block per
// tool. It lives in package main, which owns stdout, process execution, and the
// report; the gates still recurse through make for their dependency setup.
package main

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"goodkind.io/go-makefile/internal/logsummary"
	"goodkind.io/go-makefile/internal/report"
)

// runBuildCheck runs vet, the lint gates, and govulncheck, then prints one
// report. GO_MK_LOG=debug falls back to streaming each phase for troubleshooting.
// It returns the exit code, non-zero when any step failed.
func runBuildCheck() int {
	if logsummary.ParseMode(os.Getenv("GO_MK_LOG")) == logsummary.ModeDebug {
		return runBuildCheckRaw()
	}

	steps := make([]report.StepResult, 0, 8)
	diag := make(map[string]int)
	status := 0

	vetResult, vetStatus := runVetStep()
	steps = append(steps, vetResult)
	if vetStatus != 0 {
		status = vetStatus
	}

	gateSteps, gateDiag, gateStatus, err := collectGateSteps()
	if err != nil {
		return statusFromError(err)
	}
	steps = append(steps, gateSteps...)
	mergeCounts(diag, gateDiag)
	if gateStatus != 0 {
		status = gateStatus
	}

	vulnResult, vulnStatus := runGovulncheckStep()
	steps = append(steps, vulnResult)
	if vulnStatus != 0 {
		status = vulnStatus
	}

	mergeCounts(diag, logsummary.Counts())
	writeStdout(report.Render(report.Report{
		Title:           "go-mk build-check",
		Steps:           steps,
		DiagnosticsLine: diagnosticsLine(diag),
	}))
	return applyGoVersionNotice(status)
}

// runBuildCheckRaw streams vet, the gates, and govulncheck for the
// GO_MK_LOG=debug path, mirroring the historical separate-target behaviour.
func runBuildCheckRaw() int {
	status := 0
	if err := runVet(); err != nil {
		status = statusFromError(err)
	}
	if code := runLintChain(); code != 0 {
		status = code
	}
	if err := runGovulncheck(); err != nil {
		status = statusFromError(err)
	}
	return applyGoVersionNotice(status)
}

// runVetStep runs go vet as a captured build-check step.
func runVetStep() (report.StepResult, int) {
	targets := splitWords(lintEnvDefault("GO_VET_TARGETS", "./..."))
	return toolStep("vet", "go", append([]string{"vet"}, targets...))
}

// runGovulncheckStep installs and runs govulncheck as a captured build-check
// step, mirroring runGovulncheck but collecting a StepResult.
func runGovulncheckStep() (report.StepResult, int) {
	if err := installGoTool("golang.org/x/vuln/cmd/govulncheck@latest"); err != nil {
		return toolFailure("govulncheck", err), 1
	}
	gopathBin, err := goEnvPath("GOPATH")
	if err != nil {
		return toolFailure("govulncheck", err), 1
	}
	targets := splitWords(lintEnvDefault("GOVULNCHECK_TARGETS", "./..."))
	return toolStep("govulncheck", filepath.Join(gopathBin, "bin", "govulncheck"), targets)
}

// toolStep runs a captured tool and turns its outcome into a StepResult plus an
// exit code. On failure the captured output becomes the step's findings so
// nothing is hidden. It runs a process, so it emits a boundary log.
func toolStep(name, binary string, args []string) (report.StepResult, int) {
	slog.Info("build-check run tool", slog.String("tool", name))
	cmd := exec.Command(binary, args...)
	cmd.Env = lintEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return report.StepResult{Name: name, Status: report.StatusOK}, 0
	}
	lines := splitOutputLines(string(out))
	code := 1
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else {
		lines = append(lines, err.Error())
	}
	return report.StepResult{Name: name, Status: report.StatusFailed, Findings: lines}, code
}

// toolFailure builds a failed StepResult for a tool that could not run.
func toolFailure(name string, err error) report.StepResult {
	return report.StepResult{Name: name, Status: report.StatusFailed, Findings: []string{err.Error()}}
}
