package main

import (
	"fmt"
	"log/slog"

	"goodkind.io/go-makefile/internal/notice"
)

func captureNoticeStaticcheckFindings(rawPath, findingsPath string) error {
	if err := captureStaticcheckFindings(rawPath, findingsPath, captureNotice); err != nil {
		return err
	}

	return addOtherPlatformFindings(findingsPath)
}

func restrictNoticeFindings(
	findingsPath string,
	directive noticeDirective,
	capture func(string, string) error,
) error {
	if err := notice.RestrictFindings(
		findingsPath,
		directive.gate,
		directive.rule,
		directive.introduced,
		capture,
	); err != nil {
		wrapped := fmt.Errorf("restrict notice to historical findings: %w", err)
		slog.Error("restrict notice findings", slog.Any("err", wrapped))

		return wrapped
	}

	return nil
}
