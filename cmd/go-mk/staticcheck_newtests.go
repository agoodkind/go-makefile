// New-test blocking for the staticcheck-extra gate. The advisory analyzers
// report test habits in every _test.go file. This file selects the findings on
// lines that the working tree adds relative to a base commit. The gate fails on
// those findings and keeps the findings on older lines advisory. A repository
// with existing findings needs no baseline and no cleanup before the gate
// blocks new ones. This file owns the git process boundary; boundary functions
// emit a structured slog event.
package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/report"
)

// testBlockOff is the STATICCHECK_EXTRA_TEST_BLOCK value that keeps every
// advisory finding non-blocking.
const testBlockOff = "off"

// wholeFileEnd is the end line of the range for an untracked test file. Every
// line of an untracked file is new.
const wholeFileEnd = 1 << 30

// staticcheckNewTestRemediation is the fix hint printed under blocking findings.
const staticcheckNewTestRemediation = "New test code must satisfy the test-habit analyzers. See docs/staticcheck/tests.md in go-makefile for each rule."

// defaultBranchCandidates are the remote-tracking branches tried when the
// remote HEAD reference is absent.
var defaultBranchCandidates = []string{"main", "master"}

// staticcheckBlockingFindings returns the advisory findings on lines added
// relative to the base commit, and a note for the report when blocking is not
// active. An unresolved base keeps every finding advisory: a wrong base could
// block code that the branch did not change.
func staticcheckBlockingFindings(advisory []string) (blocking []string, note string) {
	if len(advisory) == 0 {
		return nil, ""
	}
	if strings.TrimSpace(os.Getenv("STATICCHECK_EXTRA_TEST_BLOCK")) == testBlockOff {
		return nil, ""
	}
	base, ok := newTestBase()
	if !ok {
		return nil, "No base commit was resolved. New test findings are not blocked. Set STATICCHECK_EXTRA_TEST_BASE to a commit."
	}
	ranges, err := addedTestLineRanges(base)
	if err != nil {
		slog.Warn("staticcheck new-test ranges unavailable", slog.String("base", base), slog.String("error", err.Error()))
		return nil, "git diff against " + base + " failed. New test findings are not blocked."
	}
	return findings.LineFilter(advisory, ranges), ""
}

// newTestBase resolves the commit that separates old test code from new test
// code. STATICCHECK_EXTRA_TEST_BASE wins. Otherwise the base is the merge-base
// of HEAD and the default branch on origin. When HEAD is that merge-base, the
// push base from GO_MK_DIFF_BASE is used when it is an ancestor of HEAD.
func newTestBase() (string, bool) {
	if explicit := strings.TrimSpace(os.Getenv("STATICCHECK_EXTRA_TEST_BASE")); explicit != "" {
		return explicit, true
	}
	if base, ok := shallowActionsBase(); ok {
		return base, true
	}
	head, err := loggedGitOutput("staticcheck new-test git head", "rev-parse", "HEAD")
	if err != nil || head == "" {
		return "", false
	}
	base, ok := defaultBranchMergeBase()
	if !ok {
		return "", false
	}
	if base != head {
		return base, true
	}
	pushBase := strings.TrimSpace(os.Getenv("GO_MK_DIFF_BASE"))
	if pushBase != "" && pushBase != zeroSHA && gitIsAncestor(pushBase, "HEAD") {
		return pushBase, true
	}
	return base, true
}

// githubPushEvent is the subset of the GitHub Actions event payload that names
// the default branch and the commit before a push.
type githubPushEvent struct {
	Before     string `json:"before"`
	Repository struct {
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
}

// shallowActionsBase resolves the base in a shallow GitHub Actions checkout. A
// depth-one checkout has no default branch reference and no history for a
// merge-base. The function fetches one commit: the commit before the push on
// the default branch, or the tip of the default branch on any other ref. A diff
// against a single fetched commit needs no history. It returns false outside
// GitHub Actions and in a clone with full history.
func shallowActionsBase() (string, bool) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return "", false
	}
	shallow, err := loggedGitOutput("staticcheck new-test git shallow", "rev-parse", "--is-shallow-repository")
	if err != nil || shallow != "true" {
		return "", false
	}
	event, ok := readGitHubPushEvent(os.Getenv("GITHUB_EVENT_PATH"))
	if !ok || event.Repository.DefaultBranch == "" {
		return "", false
	}
	target := event.Repository.DefaultBranch
	if os.Getenv("GITHUB_REF_NAME") == target {
		if event.Before == "" || event.Before == zeroSHA {
			return "", false
		}
		target = event.Before
	}
	if _, err := loggedGitOutput("staticcheck new-test git fetch base", "fetch", "--no-tags", "--depth=1", "origin", target); err != nil {
		slog.Warn("staticcheck new-test base fetch failed", slog.String("target", target), slog.String("error", err.Error()))
		return "", false
	}
	base, err := loggedGitOutput("staticcheck new-test git fetch head", "rev-parse", "FETCH_HEAD")
	if err != nil || base == "" {
		return "", false
	}
	return base, true
}

// readGitHubPushEvent decodes the event payload file. It emits a boundary log
// before the read.
func readGitHubPushEvent(path string) (githubPushEvent, bool) {
	var event githubPushEvent
	if path == "" {
		return event, false
	}
	slog.Info("staticcheck new-test read github event", slog.String("path", path))
	data, err := os.ReadFile(path)
	if err != nil {
		return event, false
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return event, false
	}
	return event, true
}

// defaultBranchMergeBase returns the merge-base of HEAD and the default branch
// on origin. GO_MK_DEFAULT_BRANCH wins; otherwise the remote HEAD reference and
// then the conventional branch names are tried.
func defaultBranchMergeBase() (string, bool) {
	candidates := make([]string, 0, len(defaultBranchCandidates)+1)
	if configured := strings.TrimSpace(os.Getenv("GO_MK_DEFAULT_BRANCH")); configured != "" {
		candidates = append(candidates, configured)
	} else {
		remoteHead, err := loggedGitOutput("staticcheck new-test git remote head", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
		if err == nil && strings.HasPrefix(remoteHead, "origin/") {
			candidates = append(candidates, strings.TrimPrefix(remoteHead, "origin/"))
		}
		candidates = append(candidates, defaultBranchCandidates...)
	}
	for _, branch := range candidates {
		if !gitRefExists("refs/remotes/origin/" + branch) {
			continue
		}
		if base, ok := gitMergeBase(branch, "HEAD"); ok {
			return base, true
		}
	}
	return "", false
}

// gitRefExists reports whether the reference exists in the local repository.
func gitRefExists(reference string) bool {
	_, err := loggedGitOutput("staticcheck new-test git ref", "rev-parse", "--verify", "--quiet", reference)
	return err == nil
}

// addedTestLineRanges returns the line ranges that the working tree adds to
// _test.go files relative to base, plus every line of each untracked _test.go
// file. The paths are relative to the working directory, the same form the
// finding lines use.
func addedTestLineRanges(base string) ([]findings.Range, error) {
	patch, err := loggedGitOutput(
		"staticcheck new-test git diff",
		"diff", "--unified=0", "--relative", "--diff-filter=ACMR", base, "--", "*_test.go",
	)
	if err != nil {
		return nil, err
	}
	ranges := make([]findings.Range, 0)
	if patch != "" {
		ranges = append(ranges, findings.Ranges(strings.Split(patch, "\n"))...)
	}
	untracked, err := loggedGitOutput(
		"staticcheck new-test git untracked",
		"ls-files", "--others", "--exclude-standard", "--", "*_test.go",
	)
	if err != nil {
		return nil, err
	}
	for _, file := range splitNonEmptyLines(untracked) {
		ranges = append(ranges, findings.Range{File: file, Start: 1, End: wholeFileEnd})
	}
	return ranges, nil
}

// staticcheckReportNewTestFailure reports the blocking findings and records the
// gate as failed. A collecting run records them on the gate marker. A
// standalone run prints them.
func staticcheckReportNewTestFailure(blocking []string) {
	recordFailedGate("staticcheck-extra")
	if gateCollecting {
		recordGateMarker(report.GateMarker{
			Name:        "staticcheck-extra",
			Passed:      false,
			Findings:    blocking,
			Remediation: staticcheckNewTestRemediation,
		})
		return
	}
	writeStdout("staticcheck-extra: FAILED\n")
	writeStdout("  Findings in new test code: " + itoa(len(blocking)) + "\n")
	for _, line := range blocking {
		writeStdout(line + "\n")
	}
	writeStdout("  " + staticcheckNewTestRemediation + "\n")
}
