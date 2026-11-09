package main

import "os"

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
