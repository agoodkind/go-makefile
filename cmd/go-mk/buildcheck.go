// build-check orchestration for go-mk. It runs vet, every lint gate,
// govulncheck, and the Go version check in one process, collects a StepResult
// for each, and prints one report. It lives in package main, which owns stdout,
// process execution, and the report; the gates still recurse through make for
// their dependency setup.
package main

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"goodkind.io/go-makefile/internal/logsummary"
	"goodkind.io/go-makefile/internal/report"
)

// runBuildCheck runs vet, the lint gates, govulncheck, and the Go version check,
// then prints one report. GO_MK_LOG=debug streams each phase for troubleshooting.
// It returns the exit code, non-zero when any blocking step failed.
func runBuildCheck() int {
	// notice runs in-process here, the work the go-mk-notice make prerequisite
	// used to do. It is best-effort and prints only to stderr.
	runNotice()
	if logsummary.ParseMode(os.Getenv("GO_MK_LOG")) == logsummary.ModeDebug {
		return runBuildCheckRaw()
	}
	if err := prepareChecks(false); err != nil {
		return statusFromError(err)
	}
	govulncheck := prepareGovulncheckCheck(defaultGovulncheckConfig())
	return runChecks("go-mk build-check", buildCheckChecks(govulncheck))
}

// runBuildCheckRaw streams vet, the gates, govulncheck, and Go version for the
// GO_MK_LOG=debug path, mirroring the historical separate-target behaviour.
func runBuildCheckRaw() int {
	status := 0
	if code := runAcrossPlatforms(func() int { return statusFromError(runVet()) }); code != 0 {
		status = code
	}
	if code := runLintChain(); code != 0 {
		status = code
	}
	runStandaloneGovulncheck()
	runGoVersionCheck()
	return status
}

// runVetStep runs go vet as a captured build-check step.
func runVetStep() (report.StepResult, int) {
	targets := splitWords(lintEnvDefault("GO_VET_TARGETS", "./..."))
	return toolStep("vet", "go", append([]string{"vet"}, targets...))
}

// runGovulncheckStep installs and runs govulncheck as a captured advisory step.
func runGovulncheckStep() (report.StepResult, int) {
	return runGovulncheckStepWith(defaultGovulncheckConfig()), 0
}

func prepareGovulncheckCheck(config govulncheckConfig) check {
	installSpec := lintEnvDefault("GOVULNCHECK_INSTALL", defaultGovulncheckInstall)
	if err := config.install(installSpec); err != nil {
		result := advisoryToolFailure("govulncheck", "installation failed: "+err.Error())
		return check{name: "govulncheck", run: func() (report.StepResult, int) {
			return result, 0
		}}
	}
	config.install = func(string) error { return nil }
	return check{name: "govulncheck", run: func() (report.StepResult, int) {
		return runGovulncheckStepWith(config), 0
	}}
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
	installSpec := lintEnvDefault("GOVULNCHECK_INSTALL", defaultGovulncheckInstall)
	if err := config.install(installSpec); err != nil {
		return advisoryToolFailure("govulncheck", "installation failed: "+err.Error())
	}
	goBin, err := config.goPath("GOBIN")
	if err != nil {
		return advisoryToolFailure("govulncheck", "Go tool path lookup failed: "+err.Error())
	}
	if goBin == "" {
		goPathList, pathErr := config.goPath("GOPATH")
		if pathErr != nil {
			return advisoryToolFailure("govulncheck", "Go tool path lookup failed: "+pathErr.Error())
		}
		goPaths := filepath.SplitList(goPathList)
		if len(goPaths) == 0 {
			return advisoryToolFailure("govulncheck", "Go tool path lookup failed: GOPATH has no entries")
		}
		goBin = filepath.Join(goPaths[0], "bin")
	}
	targets := splitWords(lintEnvDefault("GOVULNCHECK_TARGETS", "./..."))
	output, err := config.run(filepath.Join(goBin, "govulncheck"), targets)
	if err == nil {
		return report.StepResult{Name: "govulncheck", Status: report.StatusOK}
	}
	findings := splitOutputLines(string(output))
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 3 {
		return report.StepResult{
			Name:     "govulncheck",
			Status:   report.StatusAdvisory,
			Findings: findings,
		}
	}
	findings = append(findings, "govulncheck execution failed: "+err.Error())
	return report.StepResult{
		Name:     "govulncheck",
		Status:   report.StatusAdvisory,
		Findings: findings,
	}
}

func runStandaloneGovulncheck() int {
	result, _ := runGovulncheckStep()
	writeStdout(report.Render(report.Report{
		Title: "go-mk govulncheck",
		Steps: []report.StepResult{result},
	}))
	return 0
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

func advisoryToolFailure(name string, finding string) report.StepResult {
	return report.StepResult{
		Name:     name,
		Status:   report.StatusAdvisory,
		Findings: []string{finding},
	}
}
