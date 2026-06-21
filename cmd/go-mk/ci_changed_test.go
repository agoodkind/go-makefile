package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseCIChangedConfig returns a push-event config whose seams default to a
// resolvable base, an empty diff, no source files, and no submodules. Each test
// overrides only the fields it exercises.
func baseCIChangedConfig() ciChangedConfig {
	return ciChangedConfig{
		eventName:     "push",
		base:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		head:          "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		baseInHistory: func(string) bool { return true },
		diffNames:     func(_, _ string) ([]string, error) { return nil, nil },
		sourceFiles:   func() ([]string, error) { return nil, nil },
		submoduleDirs: func() ([]string, error) { return nil, nil },
		stdout:        func(string) {},
	}
}

// runCIChangedCapture runs the detector with stdout and GITHUB_OUTPUT redirected
// to a buffer and a temp file, returning the status, the printed text, and the
// output-file contents.
func runCIChangedCapture(t *testing.T, config ciChangedConfig) (int, string, string) {
	t.Helper()
	var out strings.Builder
	config.stdout = func(text string) { out.WriteString(text) }
	outputFile := filepath.Join(t.TempDir(), "github_output.txt")
	config.outputPath = outputFile
	status := runCIChangedWith(config)
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	return status, out.String(), string(data)
}

func TestDecideChanged(t *testing.T) {
	cases := []struct {
		name    string
		inputs  ciChangeInputs
		changed bool
	}{
		{
			name: "docs only skips",
			inputs: ciChangeInputs{
				changedPaths: []string{"README.md", "docs/guide.md"},
				sourceFiles:  []string{"cmd/go-mk/main.go"},
			},
			changed: false,
		},
		{
			name: "go source in build graph runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"cmd/go-mk/main.go"},
				sourceFiles:  []string{"cmd/go-mk/main.go"},
			},
			changed: true,
		},
		{
			name: "embedded file in build graph runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"templates/page.tmpl"},
				sourceFiles:  []string{"templates/page.tmpl"},
			},
			changed: true,
		},
		{
			name: "cgo c file runs by extension",
			inputs: ciChangeInputs{
				changedPaths: []string{"internal/scan/match.c"},
				sourceFiles:  nil,
			},
			changed: true,
		},
		{
			name: "deleted go file runs by extension",
			inputs: ciChangeInputs{
				changedPaths: []string{"cmd/go-mk/removed.go"},
				sourceFiles:  []string{"cmd/go-mk/main.go"},
			},
			changed: true,
		},
		{
			name: "go mod runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"go.mod"},
			},
			changed: true,
		},
		{
			name: "workflow runs",
			inputs: ciChangeInputs{
				changedPaths: []string{".github/workflows/_ci.yml"},
			},
			changed: true,
		},
		{
			name: "submodule pointer runs",
			inputs: ciChangeInputs{
				changedPaths:  []string{"third_party/gksyntax"},
				submoduleDirs: []string{"third_party/gksyntax"},
			},
			changed: true,
		},
		{
			name: "workspace grammar source runs",
			inputs: ciChangeInputs{
				changedPaths:  []string{"third_party/gksyntax/grammar.js"},
				workspaceDirs: []string{"third_party/gksyntax"},
			},
			changed: true,
		},
		{
			name: "subdir module go.mod runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"dots/go.mod"},
			},
			changed: true,
		},
		{
			name: "second module go.mod runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"staticcheck/go.mod"},
			},
			changed: true,
		},
		{
			name: "objective-c source runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"internal/cam/capture.m"},
			},
			changed: true,
		},
		{
			name: "prebuilt syso runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"internal/rsrc/icon.syso"},
			},
			changed: true,
		},
		{
			name: "readme alone skips",
			inputs: ciChangeInputs{
				changedPaths: []string{"README.md"},
			},
			changed: false,
		},
		{
			name: "baseline file runs",
			inputs: ciChangeInputs{
				changedPaths: []string{".golangci-lint-baseline.txt"},
			},
			changed: true,
		},
		{
			name: "fetched go.mk under .make runs",
			inputs: ciChangeInputs{
				changedPaths: []string{".make/go.mk"},
			},
			changed: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			changed, reason := decideChanged(testCase.inputs)
			if changed != testCase.changed {
				t.Fatalf("decideChanged = %v (%s), want %v", changed, reason, testCase.changed)
			}
		})
	}
}

func TestRunCIChangedFailsSafeOnNonPushEvent(t *testing.T) {
	config := baseCIChangedConfig()
	config.eventName = "pull_request"
	config.diffNames = func(_, _ string) ([]string, error) {
		t.Fatal("diff must not run for a non-push event")
		return nil, nil
	}
	status, _, output := runCIChangedCapture(t, config)
	if status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if !strings.Contains(output, "changed=true") {
		t.Fatalf("output = %q, want changed=true", output)
	}
}

func TestRunCIChangedFailsSafeOnNewBranch(t *testing.T) {
	config := baseCIChangedConfig()
	config.base = zeroSHA
	status, _, output := runCIChangedCapture(t, config)
	if status != 0 || !strings.Contains(output, "changed=true") {
		t.Fatalf("status=%d output=%q, want 0 and changed=true", status, output)
	}
}

func TestRunCIChangedFailsSafeWhenBaseMissing(t *testing.T) {
	config := baseCIChangedConfig()
	config.baseInHistory = func(string) bool { return false }
	_, stdout, output := runCIChangedCapture(t, config)
	if !strings.Contains(output, "changed=true") {
		t.Fatalf("output = %q, want changed=true", output)
	}
	if !strings.Contains(stdout, "not in history") {
		t.Fatalf("stdout = %q, want force-push note", stdout)
	}
}

func TestRunCIChangedFailsSafeOnDiffError(t *testing.T) {
	config := baseCIChangedConfig()
	config.diffNames = func(_, _ string) ([]string, error) {
		return nil, errors.New("git diff boom")
	}
	_, _, output := runCIChangedCapture(t, config)
	if !strings.Contains(output, "changed=true") {
		t.Fatalf("output = %q, want changed=true", output)
	}
}

func TestRunCIChangedFailsSafeOnGoListError(t *testing.T) {
	config := baseCIChangedConfig()
	config.diffNames = func(_, _ string) ([]string, error) { return []string{"README.md"}, nil }
	config.sourceFiles = func() ([]string, error) { return nil, errors.New("go list boom") }
	_, _, output := runCIChangedCapture(t, config)
	if !strings.Contains(output, "changed=true") {
		t.Fatalf("output = %q, want changed=true", output)
	}
}

func TestRunCIChangedEmptyDiffSkips(t *testing.T) {
	config := baseCIChangedConfig()
	config.diffNames = func(_, _ string) ([]string, error) { return nil, nil }
	_, _, output := runCIChangedCapture(t, config)
	if !strings.Contains(output, "changed=false") {
		t.Fatalf("output = %q, want changed=false", output)
	}
}

func TestRunCIChangedDocsOnlySkips(t *testing.T) {
	config := baseCIChangedConfig()
	config.diffNames = func(_, _ string) ([]string, error) {
		return []string{"README.md", "docs/guide.md"}, nil
	}
	config.sourceFiles = func() ([]string, error) { return []string{"cmd/go-mk/main.go"}, nil }
	status, _, output := runCIChangedCapture(t, config)
	if status != 0 || !strings.Contains(output, "changed=false") {
		t.Fatalf("status=%d output=%q, want 0 and changed=false", status, output)
	}
}

func TestRunCIChangedGoChangeRuns(t *testing.T) {
	config := baseCIChangedConfig()
	config.diffNames = func(_, _ string) ([]string, error) {
		return []string{"cmd/go-mk/main.go"}, nil
	}
	config.sourceFiles = func() ([]string, error) { return []string{"cmd/go-mk/main.go"}, nil }
	_, _, output := runCIChangedCapture(t, config)
	if !strings.Contains(output, "changed=true") {
		t.Fatalf("output = %q, want changed=true", output)
	}
}

func TestRunCIChangedSubmoduleBumpRuns(t *testing.T) {
	config := baseCIChangedConfig()
	config.diffNames = func(_, _ string) ([]string, error) {
		return []string{"third_party/gksyntax"}, nil
	}
	config.submoduleDirs = func() ([]string, error) { return []string{"third_party/gksyntax"}, nil }
	_, _, output := runCIChangedCapture(t, config)
	if !strings.Contains(output, "changed=true") {
		t.Fatalf("output = %q, want changed=true", output)
	}
}

func TestWorkspaceTriggerDirsDropsDotAndAppliesPrefix(t *testing.T) {
	dirs := workspaceTriggerDirs(". third_party/gksyntax", "dots/")
	if len(dirs) != 1 || dirs[0] != "dots/third_party/gksyntax" {
		t.Fatalf("dirs = %v, want [dots/third_party/gksyntax]", dirs)
	}
}
