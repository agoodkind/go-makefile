package staticcheckresult

import (
	"errors"
	"os"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lint"
)

var errMissingAnalyzer = errors.New("notice requires an executable staticcheck-extra analyzer")

// Capture saves observed diagnostics from one analyzer invocation.
//
// Incomplete analysis returns an error after preserving filtered observations.
// The command layer supplies its process and filesystem operations.
//
// A normal capture uses:
//
//	Capture(raw, output, mode, noticeMode, selectBin, targets, execute, read, write,
//	    root, exclude, filter, unique, executable, stdout)
func Capture[Mode ~int](
	rawPath string,
	findingsPath string,
	mode Mode,
	noticeMode Mode,
	selectBin func() (string, error),
	targets func([]string) ([]string, error),
	execute func(string, []string, string) (int, error),
	read func(string) ([]string, error),
	write func(string, []string) error,
	root func() string,
	exclude func() string,
	filter func([]string, string) []string,
	unique func([]string) []string,
	executable func(string) bool,
	stdout func(string),
) error {
	selected, err := selectBin()
	if err != nil {
		return err
	}

	if mode == noticeMode && (selected == "" || !executable(selected)) {
		return errMissingAnalyzer
	}

	if selected == "" {
		stdout("staticcheck-extra: not configured (skipped)\n")

		return write(findingsPath, nil)
	}

	if !executable(selected) {
		stdout("staticcheck-extra: binary " + selected + " not executable; skipping\n")

		return write(findingsPath, nil)
	}

	targetText := os.Getenv("STATICCHECK_EXTRA_TARGETS")
	if targetText == "" {
		targetText = "./..."
	}

	packages, err := targets(strings.Fields(targetText))
	if err != nil {
		return err
	}

	arguments := append(strings.Fields(lint.StaticcheckFlagsText()), packages...)
	status, err := execute(selected, arguments, rawPath)
	if err != nil {
		return err
	}

	lines, err := read(rawPath)
	if err != nil {
		return err
	}

	return saveCapture(status, lines, findingsPath, root(), exclude(), filter, unique, write)
}

func saveCapture(
	status int,
	lines []string,
	path string,
	root string,
	exclude string,
	filter func([]string, string) []string,
	unique func([]string) []string,
	write func(string, []string) error,
) error {
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		normalized = append(normalized, findings.NormalizePath(line, root, root))
	}

	result := Parse(status, normalized)
	if err := write(path, unique(filter(result.Findings, exclude))); err != nil {
		return err
	}

	if !result.Complete() {
		return &IncompleteError{Result: result}
	}

	return nil
}
