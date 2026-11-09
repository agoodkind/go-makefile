// Package history restricts automatic notices to findings before their cutoff.
package history

import (
	"context"
	"strings"
	"time"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/notice"
)

// GitOutput executes a real Git query and returns its standard output.
type GitOutput func(message string, arguments ...string) (string, error)

// Capture executes the configured analyzer and writes its captured findings.
type Capture func(rawPath, findingsPath string) error

type comparison struct {
	gate    string
	rule    string
	git     GitOutput
	capture Capture
}

type sourceCache map[string]notice.Source

func (sources sourceCache) identity(finding, root string) (string, error) {
	identity, err := notice.SourceIdentity(sources, finding, root)
	if err != nil {
		return "", failed("identify notice source occurrence", err)
	}

	return identity, nil
}

// RestrictFindings keeps only unchanged occurrences before the original cutoff.
//
// Git and analyzer capture execute through the caller's real IO operations.
// Each historical occurrence can accept only one current occurrence.
//
// For example:
//
//	RestrictFindings(ctx, path, cutoff, gate, rule, git, capture)
func RestrictFindings(
	ctx context.Context,
	path string,
	introduced time.Time,
	gate string,
	rule string,
	git GitOutput,
	capture Capture,
) error {
	check := comparison{gate: gate, rule: rule, git: git, capture: capture}
	revision, err := check.revision(introduced)
	if err != nil {
		return err
	}

	current, err := readFindings(path)
	if err != nil {
		return err
	}

	historical, err := check.captureHistory(ctx, revision)
	if err != nil {
		return err
	}

	kept, err := check.eligible(current, historical)
	if err != nil {
		return err
	}

	return writeFindings(path, kept)
}

func (check comparison) revision(introduced time.Time) (string, error) {
	if introduced.IsZero() {
		return "", errMissingIntroduced
	}

	shallow, err := check.output("notice history depth", "rev-parse", "--is-shallow-repository")
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(shallow) == "true" {
		return "", errShallowHistory
	}

	cutoff := introduced.Add(-time.Second).Format(time.RFC3339)
	revision, err := check.output(
		"notice history cutoff",
		"log",
		"--first-parent",
		"-1",
		"--format=%H",
		"--before="+cutoff,
		"HEAD",
	)
	if err != nil {
		return "", err
	}

	revision = strings.TrimSpace(revision)
	if revision == "" {
		return "", &cutoffError{introduced: introduced}
	}

	return revision, nil
}

func (check comparison) eligible(current, historical []string) ([]string, error) {
	accepted := make(map[string]int, len(historical))
	for _, finding := range historical {
		accepted[finding]++
	}

	sources := make(sourceCache)
	rejected := make(map[string]bool)

	for _, finding := range current {
		identity, err := notice.Identity(finding, ".", check.gate, check.rule, sources.identity)
		if err != nil {
			return nil, failed("compare current notice source occurrence", err)
		}

		if accepted[identity] == 0 {
			rejected[baseline.Key(finding)] = true

			continue
		}

		accepted[identity]--
	}

	kept := make([]string, 0, len(current))
	for _, finding := range current {
		if !rejected[baseline.Key(finding)] {
			kept = append(kept, finding)
		}
	}

	return kept, nil
}
