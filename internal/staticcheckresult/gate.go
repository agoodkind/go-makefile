package staticcheckresult

import (
	"regexp"

	"goodkind.io/go-makefile/internal/lintgate"
)

// Evaluate compares observed diagnostics with the configured baseline and
// filters.
//
// An incomplete analysis still fails after matching every observed finding.
// Unseen baseline rows cannot prove that source findings were fixed.
//
// Partial analysis uses the same comparison:
//
//	gate := Evaluate(result, baseline, exclude, scope, remediation)
func Evaluate(
	result Result,
	baseline []string,
	exclude *regexp.Regexp,
	scope *regexp.Regexp,
	remediation string,
) lintgate.GateResult {
	gate := lintgate.Evaluate(
		"staticcheck-extra",
		result.Findings,
		baseline,
		exclude,
		scope,
		remediation,
	)
	if !result.Complete() {
		gate.Passed = false
		gate.GoneCount = 0
		gate.SuppressFixedCount = true
	}

	return gate
}
