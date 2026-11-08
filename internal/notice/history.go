package notice

import (
	"context"
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

const historyMakeDirectory = ".make"

// RestrictFindings retains findings that existed before the notice effective date.
func RestrictFindings(
	findingsPath string,
	gate string,
	rule string,
	introduced time.Time,
	capture func(string, string) error,
) error {
	if introduced.IsZero() {
		return fmt.Errorf("auto-baseline requires a notice with a valid INTRODUCED date")
	}

	shallow, err := historyOutput("notice history depth", "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}

	if strings.TrimSpace(shallow) == "true" {
		return fmt.Errorf("auto-baseline requires full Git history; fetch the missing history and retry")
	}

	cutoff := introduced.Add(-time.Second).Format(time.RFC3339)
	revision, err := historyOutput("notice history cutoff", "log", "-1", "--format=%H", "--before="+cutoff, "HEAD")
	if err != nil {
		return err
	}

	revision = strings.TrimSpace(revision)
	if revision == "" {
		return fmt.Errorf("auto-baseline cannot find a commit before %s", introduced.Format(time.RFC3339))
	}

	current, err := readHistoryFindings(findingsPath)
	if err != nil {
		return err
	}

	historical, err := captureHistory(revision, gate, rule, capture)
	if err != nil {
		return err
	}

	accepted := make(map[string]int, len(historical))
	for _, finding := range historical {
		accepted[finding]++
	}

	sources := make(findingSources)
	rejected := make(map[string]bool)

	for _, finding := range current {
		identity, err := findingIdentity(finding, ".", sources, gate, rule)
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

	return writeHistoryFindings(findingsPath, kept)
}

func captureHistory(
	revision string,
	gate string,
	rule string,
	capture func(string, string) error,
) ([]string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, failure("read current notice directory", err)
	}

	repository, err := historyOutput("notice history root", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}

	repository = strings.TrimSpace(repository)
	prefix, err := historyOutput("notice history module", "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}

	prefix = strings.TrimSpace(prefix)
	renames, err := historyRenames(revision)
	if err != nil {
		return nil, err
	}

	modulePath, err := historicalModule(revision, prefix+"go.mod")
	if err != nil {
		return nil, err
	}

	temporaryDir, err := os.MkdirTemp("", "go-mk-notice-history-")
	if err != nil {
		return nil, failure("create notice history checkout", err)
	}

	defer func() {
		if err := os.RemoveAll(temporaryDir); err != nil {
			slog.Error("notice remove history checkout", slog.String("err", err.Error()))
		}
	}()
	checkout := filepath.Join(temporaryDir, "checkout")
	if err := historyGit("", "clone", "--shared", "--no-checkout", "--quiet", "--", repository, checkout); err != nil {
		return nil, err
	}

	if err := historyGit(checkout, "checkout", "--quiet", "--detach", revision); err != nil {
		return nil, err
	}

	moduleDir, err := filepath.EvalSymlinks(filepath.Join(checkout, filepath.Dir(modulePath)))
	if err != nil {
		return nil, failure("resolve notice history module", err)
	}

	if err := StageTools(filepath.Join(workingDir, historyMakeDirectory), filepath.Join(moduleDir, historyMakeDirectory)); err != nil {
		return nil, err
	}

	findingsPath := filepath.Join(temporaryDir, "findings.out")
	if err := CaptureInDirectory(moduleDir, filepath.Join(temporaryDir, "raw.out"), findingsPath, capture); err != nil {
		return nil, err
	}

	lines, err := readHistoryFindings(findingsPath)
	if err != nil {
		return nil, err
	}

	sources := make(findingSources)

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
				return nil, failure("resolve renamed notice source", err)
			}
		}

		identity, err := findingIdentity(finding, moduleDir, sources, gate, rule)
		if err != nil {
			return nil, err
		}

		currentFinding := filepath.ToSlash(relative) + strings.TrimPrefix(finding, file)
		identity = strings.Replace(identity, baseline.Key(finding), baseline.Key(currentFinding), 1)
		historical = append(historical, identity)
	}

	return historical, nil
}

func historicalModule(revision, currentPath string) (string, error) {
	output, err := historyOutput("notice follow module renames", "log", "--follow", "--diff-filter=R", "--format=", "--name-status", "-z", revision+"..HEAD", "--", "go.mod")
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

func historyRenames(revision string) (map[string]string, error) {
	output, err := historyOutput("notice history renames", "diff", "--name-status", "-z", "--find-renames", revision, "HEAD")
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

func historyGit(directory string, args ...string) error {
	slog.Info("notice history git", slog.String("operation", args[0]))
	command := exec.CommandContext(context.Background(), "git", args...)
	command.Dir = directory
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	if err := command.Run(); err != nil {
		return failure("run notice history Git command", err)
	}

	return nil
}
