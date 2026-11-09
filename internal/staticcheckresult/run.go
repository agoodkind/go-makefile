package staticcheckresult

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
	"goodkind.io/go-makefile/internal/lintgate"
)

const component = "staticcheck-extra"

// RunGate compares captured diagnostics and preserves incomplete failures.
//
// Complete runs use the existing gate renderer. Partial runs compare observed
// findings and report analysis errors without counting unseen findings fixed.
//
// The command layer supplies its existing boundary operations:
//
//	RunGate(directory, capture, read, baseline, patterns, exclude, complete,
//	    collecting, recordFailure, recordFailed, stdout, status,
//	    ensure, notices, prepared, resolve)
func RunGate(
	directory string,
	capture func(string, string) error,
	read func(string) ([]string, error),
	baseline func(string, string, string, string) ([]string, error),
	patterns func(string, string) (*regexp.Regexp, *regexp.Regexp, error),
	exclude func() string,
	complete func(string, []string, string, string, string, string, bool) (bool, error),
	collecting bool,
	recordFailure func([]string),
	recordFailed func(string),
	stdout func(string),
	status func(error) int,
	ensure func() error,
	notices func(),
	prepared bool,
	resolve func() int,
) int {
	if code := prepareGate(ensure, notices, prepared, resolve, status); code != 0 {
		return code
	}

	analysis, err := captureObserved(directory, capture, read)
	if err != nil {
		return status(err)
	}

	excludePattern := exclude()
	flags := lint.StaticcheckFlagsText()
	scope := lint.StaticcheckScopePattern(
		os.Getenv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN"),
		flags,
	)
	baselinePath := os.Getenv("STATICCHECK_EXTRA_BASELINE")
	if baselinePath == "" {
		baselinePath = ".staticcheck-extra-baseline.txt"
	}

	if analysis.Complete() {
		passed, err := complete(
			component,
			analysis.Findings,
			baselinePath,
			"Fix the new findings before this gate will pass.",
			excludePattern,
			scope,
			lint.StaticcheckSuppressFixed(flags, scope),
		)
		if err != nil {
			return status(err)
		}

		if !passed {
			return 1
		}

		return 0
	}

	gate, err := incompleteGate(analysis, baselinePath, excludePattern, scope, baseline, patterns)
	if err != nil {
		return status(err)
	}

	lines := incompleteLines(analysis, gate)
	if collecting {
		recordFailure(lines)
	} else {
		stdout(component + ": FAILED\n")

		for _, line := range lines {
			stdout("  " + line + "\n")
		}
	}

	recordFailed(component)

	return 1
}

func prepareGate(
	ensure func() error,
	notices func(),
	prepared bool,
	resolve func() int,
	status func(error) int,
) int {
	if err := ensure(); err != nil {
		return status(err)
	}

	notices()
	if prepared {
		return 0
	}

	return resolve()
}

func captureObserved(
	directory string,
	capture func(string, string) error,
	read func(string) ([]string, error),
) (Result, error) {
	findingsPath := filepath.Join(directory, "staticcheck-extra.out")
	analysis := Result{Status: 0, Findings: nil, PackageErrors: nil, ToolErrors: nil}
	var failure *IncompleteError
	if err := capture(
		filepath.Join(directory, "staticcheck-extra.raw.out"),
		findingsPath,
	); err != nil {
		if !errors.As(err, &failure) {
			return analysis, err
		}

		analysis = failure.Result
	}

	current, err := read(findingsPath)
	if err != nil {
		return analysis, err
	}

	analysis.Findings = current

	return analysis, nil
}

func incompleteGate(
	analysis Result,
	baselinePath string,
	exclude string,
	scope string,
	baseline func(string, string, string, string) ([]string, error),
	patterns func(string, string) (*regexp.Regexp, *regexp.Regexp, error),
) (lintgate.GateResult, error) {
	saved, err := baseline(baselinePath, component, exclude, scope)
	if err != nil {
		return lintgate.GateResult{}, err
	}

	excludePattern, scopePattern, err := patterns(exclude, scope)
	if err != nil {
		return lintgate.GateResult{}, err
	}

	return Evaluate(
		analysis,
		saved,
		excludePattern,
		scopePattern,
		"Fix the analysis errors and any new findings before rerunning this gate.",
	), nil
}

func incompleteLines(analysis Result, gate lintgate.GateResult) []string {
	lines := analysis.FailureLines()
	lines = append(lines, "Observed new findings: "+strconv.Itoa(len(gate.NewFindings)))
	if len(gate.NewFindings) > 0 {
		lines = append(lines, "Observed lint findings:")

		for _, finding := range gate.NewFindings {
			printed := strings.TrimSuffix(findings.Print(finding, "", ""), "\n")
			lines = append(lines, strings.Split(printed, "\n")...)
		}
	}

	return append(lines, "Fix: "+gate.Remediation)
}
