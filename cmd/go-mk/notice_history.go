package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/findings"
)

var activeNoticeDirective noticeDirective

func noticeFindingIdentity(finding, root string, sources noticeSources) (string, error) {
	if activeNoticeDirective.gate == "golangci" && activeNoticeDirective.rule == "file-length-limit" {
		return baseline.Key(finding), nil
	}
	return sources.identity(finding, root)
}

func captureNoticeStaticcheckFindings(rawPath, findingsPath string) error {
	if err := captureStaticcheckFindings(rawPath, findingsPath, captureNotice); err != nil {
		return err
	}
	return addOtherPlatformFindings(findingsPath)
}

// A notice can accept only findings that also existed before its effective date.
// Analyze that commit with the current tools, then compare baseline keys.
func restrictNoticeFindings(findingsPath string, capture func(string, string) error) error {
	if activeNoticeDirective.introduced.IsZero() {
		return fmt.Errorf("auto-baseline requires a notice with a valid INTRODUCED date")
	}
	shallow, err := loggedGitOutput("notice history depth", "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(shallow) == "true" {
		return fmt.Errorf("auto-baseline requires full Git history; fetch the missing history and retry")
	}
	cutoff := activeNoticeDirective.introduced.Add(-time.Second).Format(time.RFC3339)
	revision, err := loggedGitOutput("notice history cutoff", "log", "--first-parent", "-1", "--format=%H", "--before="+cutoff, "HEAD")
	if err != nil {
		return err
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return fmt.Errorf("auto-baseline cannot find a commit before %s", activeNoticeDirective.introduced.Format(time.RFC3339))
	}
	current, err := readFileLines(findingsPath)
	if err != nil {
		return err
	}
	historical, err := captureNoticeHistory(revision, capture)
	if err != nil {
		return err
	}
	accepted := make(map[string]int, len(historical))
	for _, finding := range historical {
		accepted[finding]++
	}
	sources := make(noticeSources)
	rejected := make(map[string]bool)
	for _, finding := range current {
		identity, err := noticeFindingIdentity(finding, ".", sources)
		if err != nil {
			return err
		}
		if accepted[identity] == 0 {
			rejected[baseline.Key(finding)] = true
			continue
		}
		accepted[identity]--
	}
	kept := make([]string, 0, len(current))
	for _, finding := range current {
		if !rejected[baseline.Key(finding)] {
			kept = append(kept, finding)
		}
	}
	return writeFindingsFile(findingsPath, kept)
}

func captureNoticeHistory(revision string, capture func(string, string) error) ([]string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	repository, err := loggedGitOutput("notice history root", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	repository = strings.TrimSpace(repository)
	prefix, err := loggedGitOutput("notice history module", "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	prefix = strings.TrimSpace(prefix)
	renames, err := noticeHistoryRenames(revision)
	if err != nil {
		return nil, err
	}
	modulePath, err := noticeHistoricalModule(revision, prefix+"go.mod")
	if err != nil {
		return nil, err
	}
	temporaryDir, err := os.MkdirTemp("", "go-mk-notice-history-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := os.RemoveAll(temporaryDir); err != nil {
			slog.Error("notice remove history checkout", slog.String("err", err.Error()))
		}
	}()
	checkout := filepath.Join(temporaryDir, "checkout")
	if err := noticeHistoryGit("", "clone", "--shared", "--no-checkout", "--quiet", "--", repository, checkout); err != nil {
		return nil, err
	}
	if err := noticeHistoryGit(checkout, "checkout", "--quiet", "--detach", revision); err != nil {
		return nil, err
	}
	moduleDir, err := filepath.EvalSymlinks(filepath.Join(checkout, filepath.Dir(modulePath)))
	if err != nil {
		return nil, err
	}
	if err := stageNoticeTools(filepath.Join(workingDir, makeDir), filepath.Join(moduleDir, makeDir)); err != nil {
		return nil, err
	}
	findingsPath := filepath.Join(temporaryDir, "findings.out")
	if err := captureNoticeInDirectory(moduleDir, filepath.Join(temporaryDir, "raw.out"), findingsPath, capture); err != nil {
		return nil, err
	}
	lines, err := readFileLines(findingsPath)
	if err != nil {
		return nil, err
	}
	sources := make(noticeSources)
	historical := make([]string, 0, len(lines))
	for _, finding := range lines {
		file, ok := findings.FindingPath(finding)
		if !ok {
			continue
		}
		repositoryPath := filepath.ToSlash(filepath.Join(filepath.Dir(modulePath), file))
		relative := file
		if renamed, ok := renames[repositoryPath]; ok {
			relative, err = filepath.Rel(filepath.FromSlash(prefix), filepath.FromSlash(renamed))
			if err != nil {
				return nil, err
			}
		}
		identity, err := noticeFindingIdentity(finding, moduleDir, sources)
		if err != nil {
			return nil, err
		}
		currentFinding := filepath.ToSlash(relative) + strings.TrimPrefix(finding, file)
		identity = strings.Replace(identity, baseline.Key(finding), baseline.Key(currentFinding), 1)
		historical = append(historical, identity)
	}
	return historical, nil
}

func noticeHistoricalModule(revision, currentPath string) (string, error) {
	output, err := loggedGitOutput("notice follow module renames", "log", "--follow", "--diff-filter=R", "--format=", "--name-status", "-z", revision+"..HEAD", "--", "go.mod")
	if err != nil {
		return "", err
	}
	fields := strings.Split(strings.Trim(output, "\x00\n"), "\x00")
	for index := 0; index+2 < len(fields); index += 3 {
		if fields[index+2] == currentPath {
			currentPath = fields[index+1]
		}
	}
	return currentPath, nil
}

func captureNoticeInDirectory(directory, rawPath, findingsPath string, capture func(string, string) error) (captureErr error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, name := range []string{"STATICCHECK_EXTRA_BIN", "GOLANGCI_LINT"} {
		previous, had := os.LookupEnv(name)
		if previous == "" {
			continue
		}
		resolved, err := exec.LookPath(previous)
		if err != nil {
			return err
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			return err
		}
		if err := os.Setenv(name, absolute); err != nil {
			return err
		}
		defer restoreEnv(name, previous, had)
	}
	previousRoot, hadRoot := os.LookupEnv("_GO_MK_ROOT")
	if err := os.Setenv("_GO_MK_ROOT", directory); err != nil {
		return err
	}
	defer restoreEnv("_GO_MK_ROOT", previousRoot, hadRoot)
	if err := os.Chdir(directory); err != nil {
		return err
	}
	defer func() {
		if err := os.Chdir(workingDir); err != nil {
			captureErr = errors.Join(captureErr, fmt.Errorf("restore notice working directory: %w", err))
		}
	}()
	if err := prepareNoticeHistory(); err != nil {
		return err
	}
	return capture(rawPath, findingsPath)
}

func noticeHistoryRenames(revision string) (map[string]string, error) {
	output, err := loggedGitOutput("notice history renames", "diff", "--name-status", "-z", "--find-renames", revision, "HEAD")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	renames := make(map[string]string)
	for index := 0; index+1 < len(fields); {
		status := fields[index]
		previous := fields[index+1]
		index += 2
		if !strings.HasPrefix(status, "R") {
			continue
		}
		if index >= len(fields) {
			return nil, fmt.Errorf("notice history contains an incomplete rename")
		}
		renames[previous] = fields[index]
		index++
	}
	return renames, nil
}

func noticeHistoryGit(directory string, args ...string) error {
	slog.Info("notice history git", slog.String("operation", args[0]))
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}
