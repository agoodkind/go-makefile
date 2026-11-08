package notice

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

func historyOutput(message string, args ...string) (string, error) {
	slog.Info(message)
	command := exec.CommandContext(context.Background(), "git", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", failure(message+": "+strings.TrimSpace(string(output)), err)
	}

	return string(output), nil
}

func readHistoryFindings(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, failure("read notice findings "+path, err)
	}

	trimmed := strings.TrimSuffix(string(contents), "\n")
	if trimmed == "" {
		return []string{}, nil
	}

	return strings.Split(trimmed, "\n"), nil
}

func writeHistoryFindings(path string, lines []string) error {
	slog.Info("notice write historical findings", slog.String("path", path))
	var contents strings.Builder

	for _, line := range lines {
		contents.WriteString(line)
		contents.WriteByte('\n')
	}

	if err := os.WriteFile(path, []byte(contents.String()), 0o600); err != nil {
		return failure("write notice findings", err)
	}

	return nil
}
