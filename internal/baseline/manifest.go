package baseline

import (
	"os"
	"strings"
)

// Component describes one baseline to rewrite in a batch. The shell builds this
// after capturing findings and passing the token gate.
type Component struct {
	Title          string `json:"title"`
	Label          string `json:"label"`
	BaselineFile   string `json:"baselineFile"`
	FindingsFile   string `json:"findingsFile"`
	Mode           string `json:"mode"`
	ExcludePattern string `json:"excludePattern"`
	ScopePattern   string `json:"scopePattern"`
}

// Manifest is the batch of components to write in one process so a single
// roll-up prints. Now is optional; when empty the binary stamps the current UTC
// time. Tests pin Now for deterministic output.
type Manifest struct {
	Now        string      `json:"now"`
	Components []Component `json:"components"`
}

// PlannedWrite is the result of planning one component: its neutral statistics
// and the exact file contents to persist. Planning performs no writes, so the
// file I/O boundary stays in the command layer.
type PlannedWrite struct {
	Stats    Stats
	Path     string
	Contents string
}

// readLines reads a file into lines, dropping the trailing empty element when
// the file ends in a newline, matching how awk getline yields lines. A missing
// file reads as no lines, matching the shell's `: > file` empty baseline. The
// returned error is the os error verbatim, which already names the path.
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// PlanComponent reads a component's inputs, computes the rewritten baseline
// contents and the neutral statistics, and returns both without writing. The
// statistics compare the pre-write key set to the key set of the freshly
// rendered body, preserving the pre/post split the counts depend on.
func PlanComponent(component Component, now string) (PlannedWrite, error) {
	mode, err := ParseMode(component.Mode)
	if err != nil {
		return PlannedWrite{}, err
	}

	oldLines, err := readLines(component.BaselineFile)
	if err != nil {
		return PlannedWrite{}, err
	}
	findingsLines, err := readLines(component.FindingsFile)
	if err != nil {
		return PlannedWrite{}, err
	}

	oldKeys, err := baselineKeySet(
		oldLines, component.Label, component.ExcludePattern, component.ScopePattern)
	if err != nil {
		return PlannedWrite{}, err
	}

	body, err := RewriteBody(RewriteInput{
		CurrentLines: findingsLines,
		OldLines:     oldLines,
		Label:        component.Label,
		Now:          now,
		ScopePattern: component.ScopePattern,
		Mode:         mode,
	})
	if err != nil {
		return PlannedWrite{}, err
	}

	newKeys, err := baselineKeySet(
		body, component.Label, component.ExcludePattern, component.ScopePattern)
	if err != nil {
		return PlannedWrite{}, err
	}

	stats := computeStats(
		component.Label, component.BaselineFile, component.ScopePattern,
		findingsLines, oldKeys, newKeys)
	return PlannedWrite{
		Stats:    stats,
		Path:     component.BaselineFile,
		Contents: RenderFile(component.Title, now, body),
	}, nil
}
