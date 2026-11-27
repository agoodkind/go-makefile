// build-check orchestration for go-mk. It runs vet, every lint gate,
// and the Go version check in one process, collects a StepResult
// for each, and prints one report. It lives in package main, which owns stdout,
// process execution, and the report; the gates still recurse through make for
// their dependency setup.
package main

import (
	"log/slog"
	"os/exec"

	"goodkind.io/go-makefile/internal/checkrun"
	"goodkind.io/go-makefile/internal/report"
	"goodkind.io/go-makefile/internal/vet"
)

// runBuildCheck runs vet, the lint gates, and the Go version check,
// then prints one report. GO_MK_LOG=debug streams each phase for troubleshooting.
// It returns the exit code, non-zero when any blocking step failed.
func runBuildCheck() int {
	return checkrun.Build(
		runNoticeFor,
		prepareChecks,
		statusFromError,
		runChecks,
		buildCheckChecks,
		runBuildCheckRaw,
	)
}

// runBuildCheckRaw streams vet, the gates, and Go version for the
// GO_MK_LOG=debug path, mirroring the historical separate-target behaviour.
func runBuildCheckRaw() int {
	return checkrun.Raw(
		runAcrossPlatforms,
		runVet,
		statusFromError,
		runLintChain,
		runGoVersionCheck,
	)
}

// runVetStep runs go vet as a captured build-check step.
func runVetStep() (report.StepResult, int) {
	return vet.Step(vetCommands, toolStep)
}

type govulncheckConfig struct {
	install func(string) error
	goPath  func(string) (string, error)
	run     func(string, []string) ([]byte, error)
}

func defaultGovulncheckConfig() govulncheckConfig {
	return govulncheckConfig{
		install: installGoTool,
		goPath:  goEnvPath,
		run: func(binary string, args []string) ([]byte, error) {
			slog.Info("build-check run tool", slog.String("tool", "govulncheck"))
			command := exec.Command(binary, args...)
			command.Env = lintEnv()

			return command.CombinedOutput()
		},
	}
}

func runGovulncheckStepWith(config govulncheckConfig) report.StepResult {
	return checkrun.ScanVulnerabilities(
		defaultGovulncheckInstall,
		lintEnvDefault,
		config.install,
		config.goPath,
		config.run,
	)
}

func runStandaloneGovulncheck() int {
	return checkrun.PrintStep(
		checkrun.VulnerabilityTitle,
		runGovulncheckStepWith(defaultGovulncheckConfig()),
		writeStdout,
	)
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
