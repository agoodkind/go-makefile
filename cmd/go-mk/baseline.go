// Baseline orchestration for go-mk, ported from scripts/go-mk-baseline.sh: the
// per-component updaters that pass the token gate, capture the current
// findings, and queue one manifest record each, then flush every queued record
// in one writeManifest call so a single neutral roll-up prints. This file lives
// in package main and owns the gate decision, process execution, and file I/O;
// the pure rewrite and statistics logic stays in internal/baseline and the gate
// decision logic in internal/gate.
package main

import (
	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/gate"
	"goodkind.io/go-makefile/internal/lint"
	"os"
)

// baselineMode is the named enum of baseline update modes, mirroring the shell
// normalize_mode case arm. A named string type keeps the validation switch off
// bare string literals, satisfying the bare-string-switch analyzer.
type baselineMode string

const (
	modeSync        baselineMode = "sync"
	modePruneFixed  baselineMode = "prune-fixed"
	modeRemoveFixed baselineMode = "remove-fixed"
	modeAcceptNew   baselineMode = "accept-new"
)

// baselineComponent is the named enum of component selectors the baseline
// subcommand dispatches, mirroring the shell case arm over $1.
type baselineComponent string

const (
	componentAll           baselineComponent = "all"
	componentGolangci      baselineComponent = "golangci"
	componentGolangciScope baselineComponent = "golangci-scope"
	componentAutoScope     baselineComponent = "auto-baseline-scope"
	componentGocyclo       baselineComponent = "gocyclo"
	componentDeadcode      baselineComponent = "deadcode"
	componentStaticcheck   baselineComponent = "staticcheck-extra"
)

// baselineCollector accumulates the manifest records queued by the updaters,
// mirroring the shell GO_MK_BASELINE_MANIFEST string. A skipped component
// queues no record, preserving the token-gating flow.
type baselineCollector struct {
	components []baseline.Component
}

// add queues one component record for the batch write.
func (collector *baselineCollector) add(component baseline.Component) {
	collector.components = append(collector.components, component)
}

// runBaseline is the baseline subcommand, mirroring scripts/go-mk-baseline.sh.
// It validates the update mode, dispatches the selected component's updater(s),
// and flushes every queued record in one write. A single component refusing or
// failing its gate does not abort the batch: each updater runs tolerantly and
// the worst non-zero status is carried to the exit, matching the shell
// `|| update_status=$?` flow where the last failure wins. It returns the
// process exit code.
func runBaseline(args []string) int {
	mode, ok := normalizeBaselineMode(os.Getenv("BASELINE_UPDATE_MODE"))
	if !ok {
		return 2
	}
	component := componentAll
	if len(args) > 0 && args[0] != "" {
		component = baselineComponent(args[0])
	}

	collector := &baselineCollector{}
	updateStatus := 0

	switch component {
	case componentAll:
		updateStatus = carryStatus(updateStatus, updateGolangciBaseline(collector, mode))
		updateStatus = carryStatus(updateStatus, updateGocycloBaseline(collector, mode))
		updateStatus = carryStatus(updateStatus, updateDeadcodeBaseline(collector, mode))
		updateStatus = carryStatus(updateStatus, updateStaticcheckBaseline(collector, mode))
	case componentGolangci:
		updateStatus = carryStatus(updateStatus, updateGolangciBaseline(collector, mode))
	case componentGolangciScope:
		updateStatus = carryStatus(updateStatus, updateGolangciBaselineScope(collector, mode))
	case componentAutoScope:
		updateStatus = carryStatus(updateStatus, autoBaselineGolangciScope(collector))
	case componentGocyclo:
		updateStatus = carryStatus(updateStatus, updateGocycloBaseline(collector, mode))
	case componentDeadcode:
		updateStatus = carryStatus(updateStatus, updateDeadcodeBaseline(collector, mode))
	case componentStaticcheck:
		updateStatus = carryStatus(updateStatus, updateStaticcheckBaseline(collector, mode))
	default:
		writeStdout("go-mk: unknown component " + string(component) + "\n")
		return 2
	}

	if err := flushBaseline(collector); err != nil {
		writeStderr("go-mk: " + err.Error() + "\n")
		updateStatus = carryStatus(updateStatus, 1)
	}
	return updateStatus
}

// carryStatus mirrors the shell `command || update_status=$?`: a non-zero result
// overwrites the carried status (last failure wins), a zero result leaves it
// unchanged.
func carryStatus(current, result int) int {
	if result != 0 {
		return result
	}
	return current
}

// normalizeBaselineMode validates the update mode, mirroring the shell
// normalize_mode: an empty mode defaults to sync, a known mode passes through,
// and an unknown mode prints the diagnostic and reports failure so the caller
// exits 2.
func normalizeBaselineMode(raw string) (string, bool) {
	mode := raw
	if mode == "" {
		mode = string(modeSync)
	}
	switch baselineMode(mode) {
	case modeSync, modePruneFixed, modeRemoveFixed, modeAcceptNew:
		return mode, true
	default:
		writeStdout("unknown baseline update mode: " + mode + "\n")
		return "", false
	}
}

// flushBaseline writes every queued component in one writeManifest call so the
// neutral counts and the single roll-up print once, mirroring the shell
// flush_manifest. An empty queue writes nothing.
func flushBaseline(collector *baselineCollector) error {
	if len(collector.components) == 0 {
		return nil
	}
	return writeManifest(baseline.Manifest{Components: collector.components}, false)
}

// baselineGatePasses reports whether the token gate opens, mirroring the shell
// run_gate decision: the confirm value must be affirmative, the token command
// must succeed, and its slugified output must match the slugified token value.
// The shell wrote a stamp file purely as in-process IPC between the gate binary
// and the calling script; running the gate in process collapses that to a
// boolean, so no stamp file is written. The token command runs only after the
// confirm check passes, so a routine make run with BASELINE_CONFIRM unset never
// invokes it.
func baselineGatePasses() bool {
	if !gate.ConfirmAccepted(os.Getenv("BASELINE_CONFIRM")) {
		return false
	}
	tokenCommand := os.Getenv("BASELINE_TOKEN_CMD")
	if tokenCommand == "" {
		tokenCommand = os.Getenv("GO_MK_GATE_TOKEN_CMD")
	}
	if tokenCommand == "" {
		return false
	}
	expectedRaw, ok := runTokenCommand(tokenCommand)
	if !ok {
		return false
	}
	return gate.TokensMatch(expectedRaw, os.Getenv("BASELINE_TOKEN"))
}

// updateGolangciBaseline captures the full golangci-lint baseline and queues its
// record, mirroring update_golangci_baseline. It returns 0 when the gate is
// closed (no record queued) and a non-zero status on a capture failure.
func updateGolangciBaseline(collector *baselineCollector, mode string) int {
	if !baselineGatePasses() {
		return 0
	}
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	findingsPath := makeDir + "/golangci-lint-baseline.out"
	rawPath := makeDir + "/golangci-lint-baseline.raw.out"
	excludePattern := golangciExcludePattern()
	if err := runLintTools(); err != nil {
		return statusFromError(err)
	}
	if err := runCaptureGolangciBaseline([]string{rawPath, findingsPath}); err != nil {
		return statusFromError(err)
	}
	collector.add(baseline.Component{
		Title:          "golangci-lint",
		Label:          "golangci-lint",
		BaselineFile:   lintEnvDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt"),
		FindingsFile:   findingsPath,
		Mode:           mode,
		ExcludePattern: excludePattern,
	})
	return 0
}

// updateGolangciBaselineScope captures the scoped slice of the golangci baseline
// for one linter or rule and queues its record, mirroring
// update_golangci_baseline_scope. It refuses to run unscoped so a scoped target
// cannot silently full-sync the whole baseline.
func updateGolangciBaselineScope(collector *baselineCollector, mode string) int {
	scopePattern := lint.GolangciScopePattern(
		os.Getenv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN"),
		os.Getenv("RULE"), os.Getenv("LINTER"),
	)
	if scopePattern == "" {
		writeStdout("golangci-lint scope baseline: set LINTER=<name>, RULE=<name>, or GOLANGCI_LINT_BASELINE_SCOPE_PATTERN\n")
		return 1
	}
	if !baselineGatePasses() {
		return 0
	}
	return writeGolangciScopeBaseline(collector, mode, scopePattern)
}

// autoBaselineGolangciScope captures the scoped golangci baseline without the
// token gate, mirroring auto_baseline_golangci_scope. It is safe token-free
// because the scoped write only adds the declared slice and leaves every other
// linter's rows untouched.
func autoBaselineGolangciScope(collector *baselineCollector) int {
	scopePattern := lint.GolangciScopePattern(
		os.Getenv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN"),
		os.Getenv("RULE"), os.Getenv("LINTER"),
	)
	if scopePattern == "" {
		writeStdout("auto-baseline: missing scope; set LINTER, RULE, or GOLANGCI_LINT_BASELINE_SCOPE_PATTERN\n")
		return 1
	}
	return writeGolangciScopeBaseline(collector, string(modeSync), scopePattern)
}

// writeGolangciScopeBaseline captures the scoped golangci findings and queues a
// record carrying the scope pattern, mirroring write_golangci_scope_baseline.
// The scope pattern reaches baseline.PlanComponent so the scoped rewrite
// preserves every out-of-scope baseline row byte-for-byte.
func writeGolangciScopeBaseline(collector *baselineCollector, mode, scopePattern string) int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	findingsPath := makeDir + "/golangci-lint-scope-baseline.out"
	rawPath := makeDir + "/golangci-lint-scope-baseline.raw.out"
	excludePattern := golangciExcludePattern()
	if err := runLintTools(); err != nil {
		return statusFromError(err)
	}
	if err := runCaptureGolangciScope([]string{rawPath, findingsPath}); err != nil {
		return statusFromError(err)
	}
	collector.add(baseline.Component{
		Title:          "golangci-lint",
		Label:          "golangci-lint",
		BaselineFile:   lintEnvDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt"),
		FindingsFile:   findingsPath,
		Mode:           mode,
		ExcludePattern: excludePattern,
		ScopePattern:   scopePattern,
	})
	return 0
}

// updateGocycloBaseline captures the gocyclo baseline and queues its record,
// mirroring update_gocyclo_baseline.
func updateGocycloBaseline(collector *baselineCollector, mode string) int {
	if !baselineGatePasses() {
		return 0
	}
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	findingsPath := makeDir + "/gocyclo-baseline.out"
	rawPath := makeDir + "/gocyclo-baseline.raw.out"
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("GOCYCLO_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("GOCYCLO_EXCLUDE_PATHS"),
	)
	if err := runCaptureGocyclo([]string{rawPath, findingsPath}); err != nil {
		return statusFromError(err)
	}
	collector.add(baseline.Component{
		Title:          "gocyclo",
		Label:          "gocyclo",
		BaselineFile:   lintEnvDefault("GOCYCLO_BASELINE", ".gocyclo-baseline.txt"),
		FindingsFile:   findingsPath,
		Mode:           mode,
		ExcludePattern: excludePattern,
	})
	return 0
}

// updateDeadcodeBaseline captures the deadcode baseline and queues its record,
// mirroring update_deadcode_baseline.
func updateDeadcodeBaseline(collector *baselineCollector, mode string) int {
	if !baselineGatePasses() {
		return 0
	}
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	findingsPath := makeDir + "/deadcode-baseline.out"
	rawPath := makeDir + "/deadcode-baseline.raw.out"
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("DEADCODE_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("DEADCODE_EXCLUDE_PATHS"),
	)
	if err := runCaptureDeadcode([]string{rawPath, findingsPath}); err != nil {
		return statusFromError(err)
	}
	collector.add(baseline.Component{
		Title:          "deadcode",
		Label:          "deadcode",
		BaselineFile:   lintEnvDefault("DEADCODE_BASELINE", ".deadcode-baseline.txt"),
		FindingsFile:   findingsPath,
		Mode:           mode,
		ExcludePattern: excludePattern,
	})
	return 0
}

// updateStaticcheckBaseline captures the staticcheck-extra baseline and queues
// its record, mirroring update_staticcheck_baseline. It keeps the refusal guard:
// flags set with no resolved baseline scope pattern refuses a sync, prune-fixed,
// or remove-fixed update so a flag-narrowed run cannot full-sync the baseline.
func updateStaticcheckBaseline(collector *baselineCollector, mode string) int {
	if !baselineGatePasses() {
		return 0
	}
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	findingsPath := makeDir + "/staticcheck-extra.out"
	rawPath := makeDir + "/staticcheck-extra.raw.out"
	excludePattern := lint.ExcludePattern(
		lintEnvDefault("STATICCHECK_EXTRA_DEFAULT_EXCLUDE_PATHS", `_test\.go:`),
		os.Getenv("STATICCHECK_EXTRA_EXCLUDE_PATHS"),
	)
	scopePattern := lint.StaticcheckScopePattern(
		os.Getenv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN"),
		os.Getenv("STATICCHECK_EXTRA_FLAGS"),
	)
	if os.Getenv("STATICCHECK_EXTRA_FLAGS") != "" && scopePattern == "" {
		switch baselineMode(mode) {
		case modeSync, modePruneFixed, modeRemoveFixed:
			writeStdout("staticcheck-extra: refusing " + mode + " baseline update with STATICCHECK_EXTRA_FLAGS but no baseline scope pattern\n")
			writeStdout("Set STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN to the intended finding regex, or unset STATICCHECK_EXTRA_FLAGS for a full baseline update.\n")
			return 1
		case modeAcceptNew:
		}
	}
	if code := runStaticcheckBin(); code != 0 {
		return code
	}
	if err := staticcheckCaptureFindings(rawPath, findingsPath); err != nil {
		return statusFromError(err)
	}
	collector.add(baseline.Component{
		Title:          "staticcheck-extra",
		Label:          "staticcheck-extra",
		BaselineFile:   lintEnvDefault("STATICCHECK_EXTRA_BASELINE", ".staticcheck-extra-baseline.txt"),
		FindingsFile:   findingsPath,
		Mode:           mode,
		ExcludePattern: excludePattern,
		ScopePattern:   scopePattern,
	})
	return 0
}
