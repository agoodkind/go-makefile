package format

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"goodkind.io/go-makefile/internal/findings"
)

var issueSummary = regexp.MustCompile(`^([0-9]+ issues?:|\* [A-Za-z0-9_]+: [0-9]+)$`)

// CaptureGolangci preserves findings after checking conflicting golines output.
//
// Only exact golines diagnostics for files that Verify just accepted can be
// removed. Original findings remain available if verification fails.
//
// The command layer supplies its existing capture operations:
//
//	CaptureGolangci(raw, output, pattern, command, capture, extract, exclude,
//	    write, resolve, analyzer)
func CaptureGolangci(
	rawPath string,
	findingsPath string,
	pattern *regexp.Regexp,
	command func(string) (string, []string, error),
	capture func(string, []string, string) (int, error),
	extract func(string, string, string) ([]string, int, error),
	exclude func() string,
	write func(string, []string) error,
	resolve func() error,
	analyzer func() (string, error),
) (int, int, error) {
	binary, args, err := command("run")
	if err != nil {
		return 0, 0, err
	}

	status, err := capture(binary, args, rawPath)
	if err != nil {
		return 0, 0, err
	}

	extracted, dropped, err := extract(rawPath, pattern.String(), exclude())
	if err != nil {
		return 0, 0, err
	}

	if err := write(findingsPath, extracted); err != nil {
		return 0, 0, err
	}

	files := golinesFiles(extracted)
	if len(files) == 0 {
		return status, dropped, nil
	}

	raw, err := os.ReadFile(rawPath)
	if err != nil {
		return status, dropped, failure("read golangci capture", err)
	}

	if status != 0 && status != 1 {
		return status, dropped, formatterFailure(status, raw)
	}

	output := filepath.Join(filepath.Dir(findingsPath), "golines-composition.out")
	verification, err := Verify(commandForFiles(files), output, resolve, analyzer, capture)
	if err != nil {
		return status, dropped, err
	}

	kept, err := retainFindings(extracted, verification)
	if err != nil {
		return status, dropped, err
	}

	if err := write(findingsPath, kept); err != nil {
		return status, dropped, err
	}

	if status == 1 && len(kept) == 0 && dropped == 0 && onlyIssueOutput(string(raw)) {
		status = 0
	}

	return status, dropped, nil
}

func golinesFiles(lines []string) []string {
	var files []string
	seen := make(map[string]bool)

	for _, line := range lines {
		if !strings.HasSuffix(line, " (golines)") {
			continue
		}

		path, found := findings.FindingPath(line)
		if found && !seen[path] {
			files = append(files, path)
			seen[path] = true
		}
	}

	return files
}

func retainFindings(lines []string, verification Verification) ([]string, error) {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		path, found := findings.FindingPath(line)
		if !strings.HasSuffix(line, " (golines)") || !found {
			kept = append(kept, line)

			continue
		}

		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, failure("resolve golines finding source", err)
		}

		if !verification.Normal[absolute] {
			kept = append(kept, line)
		}
	}

	return kept, nil
}

func onlyIssueOutput(raw string) bool {
	for line := range strings.SplitSeq(raw, "\n") {
		if line == "" || issueSummary.MatchString(line) {
			continue
		}

		if _, found := findings.FindingPath(line); !found ||
			!strings.HasSuffix(line, " (golines)") {
			return false
		}
	}

	return true
}
