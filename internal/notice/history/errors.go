package history

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

var (
	errMissingIntroduced = errors.New(
		"auto-baseline requires a notice with a valid INTRODUCED date",
	)
	errShallowHistory = errors.New(
		"auto-baseline requires full Git history; fetch the missing history and retry",
	)
	errIncompleteRename = errors.New("notice history contains an incomplete rename")
)

type cutoffError struct{ introduced time.Time }

func (err *cutoffError) Error() string {
	return "auto-baseline cannot find a commit before " + err.introduced.Format(time.RFC3339)
}

func failed(operation string, err error) error {
	wrapped := fmt.Errorf("%s: %w", operation, err)
	slog.Error("notice history failed", slog.String("operation", operation), slog.Any("error", err))

	return wrapped
}
