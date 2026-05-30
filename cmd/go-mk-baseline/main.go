// Command go-mk-baseline rewrites lint baseline files and prints a neutral
// update report. It is resolved on demand and tracks the go-makefile main
// branch, so consumers adopt the current engine on their next make run with no
// version pin. The shell captures findings and enforces the token gate before
// invoking this binary. This command layer owns the file-write boundary; the
// internal/baseline package stays pure.
package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"goodkind.io/go-makefile/internal/baseline"
)

const usage = `usage: go-mk-baseline <command> [options]

commands:
  write-batch [--json] [--manifest <path|->]   rewrite every baseline in the manifest
  -flags                                        print supported capabilities
`

// writeStdout writes user-facing output to standard output.
func writeStdout(text string) {
	_, _ = os.Stdout.WriteString(text)
}

// writeStderr writes diagnostics to standard error.
func writeStderr(text string) {
	_, _ = os.Stderr.WriteString(text)
}

func main() {
	if len(os.Args) < 2 {
		writeStderr(usage)
		os.Exit(2)
	}
	command := os.Args[1]
	if command == "-flags" || command == "--flags" {
		// The resolver greps this output to detect a stale binary that predates
		// a capability, mirroring the staticcheck-extra -flags probe.
		writeStdout("Name: write-batch\n")
		return
	}
	if command == "write-batch" {
		if err := runWriteBatch(os.Args[2:]); err != nil {
			writeStderr("go-mk-baseline: " + err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	writeStderr(usage)
	os.Exit(2)
}

func runWriteBatch(args []string) error {
	manifestPath := "-"
	emitJSON := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--json" {
			emitJSON = true
			continue
		}
		if argument == "--manifest" {
			if index+1 >= len(args) {
				return errMissingManifestPath
			}
			index++
			manifestPath = args[index]
			continue
		}
		return &unknownOptionError{option: argument}
	}
	if os.Getenv("BASELINE_OUTPUT_FORMAT") == "json" {
		emitJSON = true
	}

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}

	now := manifest.Now
	if now == "" {
		now = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}

	statistics := make([]baseline.Stats, 0, len(manifest.Components))
	for _, component := range manifest.Components {
		planned, planErr := baseline.PlanComponent(component, now)
		if planErr != nil {
			return planErr
		}
		if writeErr := writeFileAtomic(planned.Path, planned.Contents); writeErr != nil {
			return writeErr
		}
		statistics = append(statistics, planned.Stats)
	}

	if len(statistics) == 0 {
		return nil
	}
	if emitJSON {
		rendered, jsonErr := baseline.RenderJSON(statistics)
		if jsonErr != nil {
			return jsonErr
		}
		writeStdout(rendered)
		return nil
	}
	writeStdout(baseline.RenderText(statistics))
	return nil
}

func loadManifest(path string) (baseline.Manifest, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return baseline.Manifest{}, err
	}
	var manifest baseline.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return baseline.Manifest{}, err
	}
	return manifest, nil
}

// writeFileAtomic writes contents to a sibling temp file and renames it over the
// destination, matching the shell's `> file.tmp; mv file.tmp file`. This is the
// command-layer file-write boundary; it emits a structured event so the
// baseline mutation is observable in diagnostics (stderr), separate from the
// neutral report on stdout.
func writeFileAtomic(path, contents string) error {
	slog.Info("rewrite baseline file", slog.String("file", path))
	directory := filepath.Dir(path)
	if directory != "" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

type sentinelError string

func (errorValue sentinelError) Error() string { return string(errorValue) }

const errMissingManifestPath sentinelError = "--manifest requires a path"

type unknownOptionError struct {
	option string
}

func (errorValue *unknownOptionError) Error() string {
	return "unknown option " + errorValue.option
}
