// Notice handling for go-mk, ported from scripts/go-mk-notice.sh. The notice
// pass announces go-makefile changes to a consumer on a real build, and for a
// newly introduced gate or rule auto-baselines only that slice so the
// consumer's existing code is grandfathered without a token, then asks the
// consumer to review and commit the baseline. This file lives in package main
// and owns the file I/O, the diagnostics on stderr, and the in-process call to
// the baseline auto-scope updater; it has no pure internal/ counterpart because
// every step is a side effect.
package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// noticeFields holds the parsed columns of one notices.txt record, mirroring the
// tab-separated id/directive/summary the shell read with IFS=$'\t'.
type noticeFields struct {
	id        string
	directive string
	summary   string
}

// noticeDirective holds the auto-baseline scope tokens parsed from a notice
// directive, mirroring the GATE/LINTER/RULE/PATTERN case arm in the shell
// notice_run_auto_baseline. Gate defaults to golangci, the only supported gate.
type noticeDirective struct {
	gate    string
	linter  string
	rule    string
	pattern string
}

// runNotice is the notice subcommand, mirroring scripts/go-mk-notice.sh. It
// reads the notices file, rolls out the auto-baseline for any unapplied
// directive, prints each new summary once to stderr, and records the highest
// seen id. A missing notices file is a no-op. It always returns 0: a notice
// failure must never fail a consumer build, matching the shell's `|| true`
// invocation in go.mk.
func runNotice() int {
	noticesFile := lintEnvDefault("GO_MK_NOTICES_FILE", filepath.Join(makeDir, "notices.txt"))
	appliedFile := lintEnvDefault("GO_MK_APPLIED_NOTICES", ".go-mk-applied-notices")
	seenFile := filepath.Join(makeDir, ".go-mk-notice-seen")

	if _, err := os.Stat(noticesFile); err != nil {
		return 0
	}

	applied := loadAppliedNotices(appliedFile)
	lastSeen := loadLastSeen(seenFile)
	maxSeen := lastSeen

	records, err := readNoticeRecords(noticesFile)
	if err != nil {
		return 0
	}
	freshRepo := !anyConfiguredBaselineFileExists()
	for _, record := range records {
		numericID, ok := atoiNotice(record.id)
		if !ok {
			continue
		}
		directiveNotice := record.directive != "" && record.directive != "-"
		if directiveNotice && !applied[record.id] {
			if freshRepo {
				recordFreshNoticeApplied(record, appliedFile, applied)
			} else {
				runNoticeAutoBaseline(record, appliedFile, applied)
			}
		}
		freshNoticeApplied := freshRepo && directiveNotice && applied[record.id]
		if numericID > lastSeen && !freshNoticeApplied {
			writeStderr("go-makefile notice #" + record.id + ": " + record.summary + "\n")
		}
		if numericID > maxSeen {
			maxSeen = numericID
		}
	}

	if err := os.MkdirAll(makeDir, 0o755); err != nil {
		return 0
	}
	_ = writeSeenFile(seenFile, maxSeen)
	return 0
}

// anyConfiguredBaselineFileExists reports whether any configured baseline file
// is present. When none exists, notice directives are historical adoption
// context rather than changes to grandfather.
func anyConfiguredBaselineFileExists() bool {
	baselineFiles := []string{
		lintEnvDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt"),
		lintEnvDefault("GOCYCLO_BASELINE", ".gocyclo-baseline.txt"),
		lintEnvDefault("DEADCODE_BASELINE", ".deadcode-baseline.txt"),
		lintEnvDefault("STATICCHECK_EXTRA_BASELINE", ".staticcheck-extra-baseline.txt"),
	}
	for _, baselineFile := range baselineFiles {
		if baselineFileExists(baselineFile) {
			return true
		}
	}
	return false
}

// baselineFileExists treats stat failures other than "not found" as present so
// an unreadable baseline path does not make notice handling look like adoption.
func baselineFileExists(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return !os.IsNotExist(err)
	}
	return true
}

// readNoticeRecords reads the notices file into records, skipping blank lines
// and comment lines, mirroring the shell while-read loop with its empty and
// "#"-prefixed id skip. It reads a file, so it emits a boundary log.
func readNoticeRecords(path string) ([]noticeFields, error) {
	slog.Info("notice read records", slog.String("path", path))
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	records := make([]noticeFields, 0, len(lines))
	for _, line := range lines {
		fields := strings.SplitN(line, "\t", 3)
		id := fields[0]
		if id == "" || strings.HasPrefix(id, "#") {
			continue
		}
		record := noticeFields{id: id}
		if len(fields) > 1 {
			record.directive = fields[1]
		}
		if len(fields) > 2 {
			record.summary = fields[2]
		}
		records = append(records, record)
	}
	return records, nil
}

// loadAppliedNotices reads the committed applied-notice id set, mirroring the
// shell `cat applied_file` into applied_ids. A missing file yields an empty set.
// It reads a file, so it emits a boundary log.
func loadAppliedNotices(path string) map[string]bool {
	slog.Info("notice read applied", slog.String("path", path))
	applied := make(map[string]bool)
	lines, err := readFileLines(path)
	if err != nil {
		return applied
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			applied[trimmed] = true
		}
	}
	return applied
}

// loadLastSeen reads the highest previously printed notice id, mirroring the
// shell `cat seen_file`. A missing or unparsable file is zero. It reads a file,
// so it emits a boundary log.
func loadLastSeen(path string) int {
	slog.Info("notice read seen", slog.String("path", path))
	lines, err := readFileLines(path)
	if err != nil || len(lines) == 0 {
		return 0
	}
	value, ok := atoiNotice(strings.TrimSpace(lines[0]))
	if !ok {
		return 0
	}
	return value
}

// writeSeenFile records the highest seen notice id, mirroring the shell
// `printf max_seen > seen_file`. It mutates the filesystem, so it emits a
// boundary log.
func writeSeenFile(path string, maxSeen int) error {
	slog.Info("notice write seen", slog.String("path", path))
	return os.WriteFile(path, []byte(strconv.Itoa(maxSeen)+"\n"), 0o644)
}

// recordFreshNoticeApplied records a directive notice as applied without
// auto-baselining. A first-adoption repo has no previous baseline to preserve,
// so running the scoped baseline would create adoption-only churn.
func recordFreshNoticeApplied(record noticeFields, appliedFile string, applied map[string]bool) {
	if err := appendAppliedNotice(appliedFile, record.id); err != nil {
		writeStderr("go-makefile notice #" + record.id + ": could not record applied notice in " + appliedFile + "; will retry on the next run\n")
		return
	}
	applied[record.id] = true
}

// runNoticeAutoBaseline rolls out the scoped, token-free golangci auto-baseline
// for one notice directive, mirroring notice_run_auto_baseline: it parses the
// scope tokens, refuses any gate other than golangci, runs the in-process
// baseline auto-baseline-scope updater with the scope env set, records the id on
// success, and reports failure without aborting the build.
func runNoticeAutoBaseline(record noticeFields, appliedFile string, applied map[string]bool) {
	directive := parseNoticeDirective(record.directive)
	if directive.gate != "golangci" {
		writeStderr("go-makefile notice #" + record.id + ": unsupported auto-baseline gate '" + directive.gate + "'; skipping\n")
		return
	}

	golangciBaseline := lintEnvDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt")
	writeStderr("go-makefile notice #" + record.id + ": auto-baselining existing findings for " + record.directive + "\n")

	previousLinter, hadLinter := os.LookupEnv("LINTER")
	previousRule, hadRule := os.LookupEnv("RULE")
	previousPattern, hadPattern := os.LookupEnv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN")
	_ = os.Setenv("LINTER", directive.linter)
	_ = os.Setenv("RULE", directive.rule)
	_ = os.Setenv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN", directive.pattern)

	code := runBaseline([]string{string(componentAutoScope)})

	restoreEnv("LINTER", previousLinter, hadLinter)
	restoreEnv("RULE", previousRule, hadRule)
	restoreEnv("GOLANGCI_LINT_BASELINE_SCOPE_PATTERN", previousPattern, hadPattern)

	if code != 0 {
		writeStderr("go-makefile notice #" + record.id + ": auto-baseline failed; run it manually with the scoped baseline target\n")
		return
	}
	if err := appendAppliedNotice(appliedFile, record.id); err != nil {
		writeStderr("go-makefile notice #" + record.id + ": auto-baseline failed; run it manually with the scoped baseline target\n")
		return
	}
	applied[record.id] = true
	writeStderr("go-makefile notice #" + record.id + ": wrote " + golangciBaseline +
		". Review with 'git diff " + golangciBaseline + "' and commit it together with " + appliedFile + ".\n")
}

// parseNoticeDirective splits a directive into its scope tokens, mirroring the
// shell `for token in $directive` case arm. The gate defaults to golangci.
func parseNoticeDirective(directive string) noticeDirective {
	parsed := noticeDirective{gate: "golangci"}
	for _, token := range strings.Fields(directive) {
		switch {
		case strings.HasPrefix(token, "GATE="):
			parsed.gate = strings.TrimPrefix(token, "GATE=")
		case strings.HasPrefix(token, "LINTER="):
			parsed.linter = strings.TrimPrefix(token, "LINTER=")
		case strings.HasPrefix(token, "RULE="):
			parsed.rule = strings.TrimPrefix(token, "RULE=")
		case strings.HasPrefix(token, "PATTERN="):
			parsed.pattern = strings.TrimPrefix(token, "PATTERN=")
		}
	}
	return parsed
}

// appendAppliedNotice appends a notice id to the committed applied-notice file,
// mirroring the shell `printf id >> applied_file`. It mutates the filesystem, so
// it emits a boundary log.
func appendAppliedNotice(path, id string) error {
	slog.Info("notice append applied", slog.String("path", path), slog.String("id", id))
	directory := filepath.Dir(path)
	if directory != "" && directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.WriteString(id + "\n")
	return err
}

// restoreEnv restores an environment variable to its prior value, or unsets it
// when it was previously absent, so the auto-baseline scope env does not leak to
// later notices in the same process.
func restoreEnv(key, previous string, had bool) {
	if had {
		_ = os.Setenv(key, previous)
		return
	}
	_ = os.Unsetenv(key)
}

// atoiNotice parses a notice id as a base-10 integer, mirroring the shell
// numeric comparison `[[ id -gt last_seen ]]` which treats a non-numeric id as
// an error. It returns false when the value is not a valid integer.
func atoiNotice(value string) (int, bool) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}
