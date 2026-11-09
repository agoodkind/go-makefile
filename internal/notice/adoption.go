package notice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

var errNoRepository = errors.New("not a Git repository")

// Adoption reports the earliest recorded use of the shared build.
//
// Tracked bootstrap files establish adoption directly. Legacy Makefiles must
// define and fetch the shared include before they establish adoption.
//
// Inspect the current module with:
//
//	adopted, found, err := Adoption(ctx, ".")
func Adoption(ctx context.Context, directory string) (time.Time, bool, error) {
	shallow, err := adoptionGit(ctx, directory, "rev-parse", "--is-shallow-repository")
	if errors.Is(err, errNoRepository) {
		return time.Time{}, false, nil
	}

	if err != nil {
		return time.Time{}, false, err
	}

	if strings.TrimSpace(shallow) == "true" {
		return time.Time{}, false, nil
	}

	earliest := time.Time{}

	for _, name := range []string{"bootstrap.mk", "go.mk", ".go-mk-applied-notices"} {
		output, err := adoptionGit(
			ctx,
			directory,
			"log",
			"--follow",
			"--diff-filter=A",
			"--format=%cI",
			"--",
			name,
		)
		if err != nil {
			return time.Time{}, false, err
		}

		for line := range strings.FieldsSeq(output) {
			earliest = earlierAdoption(earliest, line)
		}
	}

	legacy, err := legacyAdoption(ctx, directory)
	if err != nil {
		return time.Time{}, false, err
	}

	if !legacy.IsZero() && (earliest.IsZero() || legacy.Before(earliest)) {
		earliest = legacy
	}

	return earliest, !earliest.IsZero(), nil
}

func earlierAdoption(current time.Time, value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return current
	}

	if current.IsZero() || parsed.Before(current) {
		return parsed
	}

	return current
}

func legacyAdoption(ctx context.Context, directory string) (time.Time, error) {
	prefix, err := adoptionGit(ctx, directory, "rev-parse", "--show-prefix")
	if err != nil {
		return time.Time{}, err
	}

	output, err := adoptionGit(
		ctx,
		directory,
		"log",
		"--first-parent",
		"--reverse",
		"--diff-filter=AM",
		"--format=%H%x00%cI",
		"--",
		"Makefile",
	)
	if err != nil {
		return time.Time{}, err
	}

	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		revision, date, found := strings.Cut(line, "\x00")
		if !found {
			continue
		}

		contents, err := adoptionGit(
			ctx,
			directory,
			"show",
			revision+":"+strings.TrimSpace(prefix)+"Makefile",
		)
		if err != nil {
			return time.Time{}, err
		}

		if legacyMakefile(contents) {
			return earlierAdoption(time.Time{}, date), nil
		}
	}

	return time.Time{}, nil
}

func adoptionGit(ctx context.Context, directory string, arguments ...string) (string, error) {
	slog.InfoContext(ctx, "notice inspect adoption history", slog.String("operation", arguments[0]))
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		if arguments[0] == "rev-parse" &&
			strings.HasPrefix(string(output), "fatal: not a git repository") {
			slog.InfoContext(ctx, "notice adoption has no Git repository")

			return "", errNoRepository
		}

		wrapped := fmt.Errorf(
			"inspect notice adoption with git %s: %w: %s",
			arguments[0],
			err,
			strings.TrimSpace(string(output)),
		)
		slog.ErrorContext(ctx, "notice adoption history failed", slog.Any("err", wrapped))

		return "", wrapped
	}

	return string(output), nil
}
