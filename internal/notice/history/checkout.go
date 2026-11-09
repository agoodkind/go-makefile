package history

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/notice"
)

const makeDirectory = ".make"

func (check comparison) captureHistory(ctx context.Context, revision string) ([]string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, failed("locate current notice source", err)
	}

	repository, err := check.output("notice history root", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}

	repository = strings.TrimSpace(repository)
	prefix, err := check.output("notice history module", "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}

	prefix = strings.TrimSpace(prefix)
	renames, err := check.renames(revision)
	if err != nil {
		return nil, err
	}

	modulePath, err := check.historicalModule(revision, prefix+"go.mod")
	if err != nil {
		return nil, err
	}

	temporaryDir, err := os.MkdirTemp("", "go-mk-notice-history-")
	if err != nil {
		return nil, failed("create notice history checkout", err)
	}

	defer func() {
		if err := os.RemoveAll(temporaryDir); err != nil {
			slog.Error("notice remove history checkout", slog.Any("error", err))
		}
	}()
	checkout := filepath.Join(temporaryDir, "checkout")
	if err := notice.HistoryGit(
		"",
		[]string{"clone", "--shared", "--no-checkout", "--quiet", "--", repository, checkout},
	); err != nil {
		return nil, failed("clone notice source history", err)
	}

	if err := notice.HistoryGit(
		checkout,
		[]string{"checkout", "--quiet", "--detach", revision},
	); err != nil {
		return nil, failed("checkout notice source revision", err)
	}

	moduleDir, err := filepath.EvalSymlinks(filepath.Join(checkout, filepath.Dir(modulePath)))
	if err != nil {
		return nil, failed("resolve historical notice module", err)
	}

	if err := stageTools(
		filepath.Join(workingDir, makeDirectory),
		filepath.Join(moduleDir, makeDirectory),
	); err != nil {
		return nil, err
	}

	findingsPath := filepath.Join(temporaryDir, "findings.out")
	if err := check.inDirectory(
		ctx,
		moduleDir,
		filepath.Join(temporaryDir, "raw.out"),
		findingsPath,
	); err != nil {
		return nil, err
	}

	lines, err := readFindings(findingsPath)
	if err != nil {
		return nil, err
	}

	return check.identities(lines, moduleDir, modulePath, prefix, renames)
}

func (check comparison) identities(
	lines []string,
	moduleDir string,
	modulePath string,
	prefix string,
	renames map[string]string,
) ([]string, error) {
	sources := make(sourceCache)

	historical := make([]string, 0, len(lines))
	for _, finding := range lines {
		file, ok := findings.FindingPath(finding)
		if !ok {
			continue
		}

		repositoryPath := filepath.ToSlash(filepath.Join(filepath.Dir(modulePath), file))
		relative := file
		if renamed, ok := renames[repositoryPath]; ok {
			var err error
			relative, err = filepath.Rel(filepath.FromSlash(prefix), filepath.FromSlash(renamed))
			if err != nil {
				return nil, failed("resolve renamed notice source", err)
			}
		}

		identity, err := notice.Identity(
			finding,
			moduleDir,
			check.gate,
			check.rule,
			sources.identity,
		)
		if err != nil {
			return nil, failed("identify historical notice source", err)
		}

		currentFinding := filepath.ToSlash(relative) + strings.TrimPrefix(finding, file)
		identity = strings.Replace(
			identity,
			notice.SourceKey(finding),
			notice.SourceKey(currentFinding),
			1,
		)
		historical = append(historical, identity)
	}

	return historical, nil
}
