package notice

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Adoption reports the earliest recorded use of the shared build.
//
// Tracked bootstrap files establish adoption directly. Legacy Makefiles must
// define and fetch the shared include before they establish adoption.
//
// Inspect the current module with:
//
//	adopted, found := Adoption(".")
func Adoption(directory string) (time.Time, bool) {
	shallow, err := adoptionGit(directory, "rev-parse", "--is-shallow-repository")
	if err != nil || strings.TrimSpace(shallow) == "true" {
		return time.Time{}, false
	}

	earliest := time.Time{}

	for _, name := range []string{"bootstrap.mk", "go.mk", ".go-mk-applied-notices"} {
		output, err := adoptionGit(
			directory,
			"log",
			"--follow",
			"--diff-filter=A",
			"--format=%cI",
			"--",
			name,
		)
		if err != nil {
			return time.Time{}, false
		}

		for line := range strings.FieldsSeq(output) {
			earliest = earlierAdoption(earliest, line)
		}
	}

	legacy, err := legacyAdoption(directory)
	if err != nil {
		return time.Time{}, false
	}

	if !legacy.IsZero() && (earliest.IsZero() || legacy.Before(earliest)) {
		earliest = legacy
	}

	return earliest, !earliest.IsZero()
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

func legacyAdoption(directory string) (time.Time, error) {
	prefix, err := adoptionGit(directory, "rev-parse", "--show-prefix")
	if err != nil {
		return time.Time{}, err
	}

	output, err := adoptionGit(
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

func adoptionGit(directory string, arguments ...string) (string, error) {
	slog.Info("notice inspect adoption history", slog.String("operation", arguments[0]))
	command := exec.CommandContext(context.Background(), "git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		wrapped := fmt.Errorf(
			"inspect notice adoption with git %s: %w: %s",
			arguments[0],
			err,
			strings.TrimSpace(string(output)),
		)
		slog.Error("notice adoption history failed", slog.Any("err", wrapped))

		return "", wrapped
	}

	return string(output), nil
}
