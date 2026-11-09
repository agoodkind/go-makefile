package notice

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

var errMissingGitOperation = errors.New("notice history requires a Git operation")

// HistoryGit runs a Git operation in an isolated historical checkout.
//
// A successful checkout initializes the exact submodule revisions
// recorded in that commit.
//
// Historical source preparation starts with:
//
//	HistoryGit(directory, []string{"checkout", "--detach", revision})
func HistoryGit(directory string, args []string) error {
	if len(args) == 0 {
		return errMissingGitOperation
	}

	slog.Info("notice history git", slog.String("operation", args[0]))
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		wrapped := fmt.Errorf("notice history git %s: %w", args[0], err)
		slog.Error("notice history git failed", slog.Any("err", wrapped))

		return wrapped
	}

	if args[0] == "checkout" {
		return HistoryGit(
			directory,
			[]string{"submodule", "update", "--init", "--recursive", "--checkout"},
		)
	}

	return nil
}
