package main

import (
	"time"

	"goodkind.io/go-makefile/internal/notice/execution"
)

// noticeDirective holds the auto-baseline scope tokens parsed from a notice
// directive, mirroring the GATE/LINTER/RULE/PATTERN case arm in the shell
// notice_run_auto_baseline. Gate defaults to golangci, the only supported gate.
type noticeDirective struct {
	gate       string
	linter     string
	rule       string
	pattern    string
	introduced time.Time
}

const noticeGateStaticcheck = "staticcheck-extra"

func runNotice() int {
	return runNoticeFor("")
}

func runNoticeFor(onlyGate string) int {
	return execution.New(
		loggedGitOutput,
		runLintTools,
		runStaticcheckBin,
		captureNoticeGolangciFindings,
		captureNoticeStaticcheckFindings,
		writeManifest,
	).RunDefault(
		onlyGate,
		configuredBaselineFiles(),
		currentCIProofEnv().inRun(),
		golangciExcludePattern(),
		staticcheckExcludePattern(),
	)
}
