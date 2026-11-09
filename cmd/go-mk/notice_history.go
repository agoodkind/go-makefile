package main

import (
	"fmt"
	"log/slog"
	"os"

	"goodkind.io/go-makefile/internal/notice/history"
)

func captureNoticeStaticcheckFindings(rawPath, findingsPath string) error {
	if err := captureStaticcheckFindings(rawPath, findingsPath, captureNotice); err != nil {
		return err
	}

	if err := addOtherPlatformFindings(findingsPath); err != nil {
		return err
	}

	scoped, err := readBaselineFindings(
		findingsPath,
		"staticcheck-extra",
		"",
		os.Getenv("STATICCHECK_EXTRA_BASELINE_SCOPE_PATTERN"),
	)
	if err != nil {
		return err
	}

	return writeFindingsFile(findingsPath, scoped)
}

func restrictNoticeFindings(
	findingsPath string,
	directive noticeDirective,
	capture func(string, string) error,
) error {
	if err := history.RestrictFindings(
		findingsPath,
		directive.introduced,
		directive.gate,
		directive.rule,
		loggedGitOutput,
		capture,
	); err != nil {
		slog.Error(
			"notice restrict historical findings",
			slog.String("path", findingsPath),
			slog.Any("error", err),
		)

		return fmt.Errorf("restrict notice findings in %s: %w", findingsPath, err)
	}

	return nil
}
