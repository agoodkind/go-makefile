package main

import (
	"regexp"
	"strings"
)

// staticcheck-extra exits with status 3 when it reports diagnostics and with
// status 1 when a package does not load or type-check.
const (
	staticcheckBuildFailureStatus = 1
	staticcheckDiagnosticsStatus  = 3
)

// A package that does not type-check makes every analyzer print "analysis
// skipped", and testseam prints "failed prerequisites" for each package that
// imports it. Those lines follow from the compiler errors.
var staticcheckCascadeLine = regexp.MustCompile(`^[a-z_]+: (analysis skipped due to errors in package|failed prerequisites: )`)

type staticcheckBuildError struct {
	compilerErrors []string
}

func (buildErr *staticcheckBuildError) Error() string {
	return "staticcheck-extra could not build every package:\n" + strings.Join(buildErr.compilerErrors, "\n")
}

func staticcheckBuildFailure(lines []string) *staticcheckBuildError {
	compilerErrors := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" || staticcheckCascadeLine.MatchString(line) {
			continue
		}
		compilerErrors = append(compilerErrors, line)
	}
	return &staticcheckBuildError{compilerErrors: compilerErrors}
}

func reportStaticcheckBuildFailure(buildErr *staticcheckBuildError) int {
	lines := make([]string, 0, len(buildErr.compilerErrors)+1)
	lines = append(lines, "staticcheck-extra could not build these packages. Fix the compiler errors:")
	lines = append(lines, buildErr.compilerErrors...)
	if gateCollecting {
		recordGateToolFailure(lines)
		recordFailedGate("staticcheck-extra")
		return 1
	}
	writeStdout("staticcheck-extra: FAILED\n")
	for _, line := range lines {
		writeStdout("  " + line + "\n")
	}
	recordFailedGate("staticcheck-extra")
	return 1
}
