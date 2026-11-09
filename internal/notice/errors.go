package notice

import "fmt"

type findingLocationError struct{ finding string }

func (err *findingLocationError) Error() string {
	return "notice cannot locate finding: " + err.finding
}

type sourceLocationError struct{ line, column int }

func (err *sourceLocationError) Error() string {
	return fmt.Sprintf("notice source location is invalid: %d:%d", err.line, err.column)
}

type duplicateRangeError struct {
	file       string
	start, end int
	missing    bool
}

func (err *duplicateRangeError) Error() string {
	if err.missing {
		return fmt.Sprintf(
			"notice duplicate range lacks Go declarations: %s:%d-%d",
			err.file,
			err.start,
			err.end,
		)
	}

	return fmt.Sprintf("notice duplicate range is invalid: %s:%d-%d", err.file, err.start, err.end)
}

type syntaxFieldError struct{ field string }

func (err *syntaxFieldError) Error() string {
	return "unsupported historical Go syntax field: " + err.field
}
