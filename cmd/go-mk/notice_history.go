package main

import (
	"fmt"
	"log/slog"

	"goodkind.io/go-makefile/internal/notice"
)

func captureNoticeStaticcheckFindings(rawPath, findingsPath string) error {
	if err := notice.CaptureStaticcheck(
		rawPath,
		findingsPath,
		captureStaticcheckFindings,
		captureNotice,
		addOtherPlatformFindings,
		lintRoot,
	); err != nil {
		wrapped := fmt.Errorf("capture scoped notice findings: %w", err)
		slog.Error("capture notice findings", slog.Any("err", wrapped))

		return wrapped
	}

	return nil
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
