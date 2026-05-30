// Command go-mk rewrites lint baseline files and prints a neutral
// update report. It is resolved on demand and tracks the go-makefile main
// branch, so consumers adopt the current engine on their next make run with no
// version pin. The shell captures findings and enforces the token gate before
// invoking this binary. This command layer owns the file-write boundary; the
// internal/baseline package stays pure.
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/findings"
)

const usage = `usage: go-mk <command> [options]

commands:
  write-batch [--json] [--manifest <path|->]   rewrite every baseline in the manifest
  findings --action <action> [options]         transform finding lines read from stdin
  -flags                                       print supported capabilities

findings actions (mirroring scripts/go-mk-findings.awk):
  normalize [--pwd <p>] [--cwd <c>]            strip pwd, cwd, and leading ../ from each line
  key [--pwd <p>] [--cwd <c>]                  collapse the first :line:col: to ::: after normalizing
  baseline --label <l> [--pwd <p>] [--cwd <c>] cut the trailing label marker and normalize
  map --keyfile <path> [--pwd <p>] [--cwd <c>] keep lines whose key is in the saved-key file
  print [--pwd <p>] [--cwd <c>]                render findings as indented location and message
  ranges                                       emit file<TAB>start<TAB>end spans from a unified diff
  linefilter --rangefile <path>                keep findings whose file:line falls in a range
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
		writeStdout("Name: findings\n")
		return
	}
	if command == "write-batch" {
		if err := runWriteBatch(os.Args[2:]); err != nil {
			writeStderr("go-mk: " + err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	if command == "findings" {
		if err := runFindings(os.Args[2:]); err != nil {
			writeStderr("go-mk: " + err.Error() + "\n")
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

// findingsAction is the named enum of finding transforms the findings subcommand
// exposes, one per awk action. A named string type lets the compiler reason
// about the closed set and keeps the dispatch switch off bare string literals.
type findingsAction string

const (
	actionNormalize  findingsAction = "normalize"
	actionKey        findingsAction = "key"
	actionBaseline   findingsAction = "baseline"
	actionMap        findingsAction = "map"
	actionPrint      findingsAction = "print"
	actionRanges     findingsAction = "ranges"
	actionLineFilter findingsAction = "linefilter"
)

// findingsOptions holds the parsed flags for the findings subcommand. The
// command layer fills it from argv and the findings package consumes the values,
// so the pure transforms never read argv or the process environment.
type findingsOptions struct {
	action    findingsAction
	pwd       string
	cwd       string
	label     string
	keyFile   string
	rangeFile string
}

// runFindings reads finding lines from stdin, dispatches to the matching pure
// transform in internal/findings, and writes the result to stdout. It mirrors
// the awk invocation surface in scripts/go-mk-findings.awk so the shell and this
// binary stay interchangeable. This command layer owns stdin and stdout; the
// findings package stays pure.
func runFindings(args []string) error {
	options, err := parseFindingsOptions(args)
	if err != nil {
		return err
	}
	lines, err := readLines(os.Stdin)
	if err != nil {
		return err
	}
	output, err := transformFindings(options, lines)
	if err != nil {
		return err
	}
	writeStdout(output)
	return nil
}

// parseFindingsOptions parses the findings subcommand flags from argv, defaulting
// the action to normalize as the awk BEGIN block does. Each flag maps to the
// option field it fills, so adding a flag is a one-line table entry rather than a
// new switch case.
func parseFindingsOptions(args []string) (findingsOptions, error) {
	options := findingsOptions{}
	var actionValue string
	targets := map[string]*string{
		"--action":    &actionValue,
		"--pwd":       &options.pwd,
		"--cwd":       &options.cwd,
		"--label":     &options.label,
		"--keyfile":   &options.keyFile,
		"--rangefile": &options.rangeFile,
	}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		target, ok := targets[argument]
		if !ok {
			return findingsOptions{}, &unknownOptionError{option: argument}
		}
		if index+1 >= len(args) {
			return findingsOptions{}, &missingValueError{option: argument}
		}
		index++
		*target = args[index]
	}
	if actionValue == "" {
		options.action = actionNormalize
	} else {
		options.action = findingsAction(actionValue)
	}
	return options, nil
}

// transformFindings applies the requested action to the input lines and returns
// the rendered output. Each branch calls one pure function in internal/findings
// and joins the results with the same per-line newline the awk emits.
func transformFindings(options findingsOptions, lines []string) (string, error) {
	switch options.action {
	case actionNormalize:
		return joinLines(mapLines(lines, func(line string) string {
			return findings.NormalizePath(line, options.pwd, options.cwd)
		})), nil
	case actionKey:
		return joinLines(mapLines(lines, func(line string) string {
			return findings.Key(line, options.pwd, options.cwd)
		})), nil
	case actionBaseline:
		return renderBaseline(options, lines), nil
	case actionMap:
		saved, err := loadKeySet(options.keyFile)
		if err != nil {
			return "", err
		}
		return joinLines(findings.Map(lines, saved, options.pwd, options.cwd)), nil
	case actionPrint:
		return renderPrint(options, lines), nil
	case actionRanges:
		return renderRanges(findings.Ranges(lines)), nil
	case actionLineFilter:
		ranges, err := loadRanges(options.rangeFile)
		if err != nil {
			return "", err
		}
		return joinLines(findings.LineFilter(lines, ranges)), nil
	default:
		return "", &unknownActionError{action: string(options.action)}
	}
}

// renderBaseline applies the baseline transform and drops the lines the awk
// skips, joining the kept payloads with trailing newlines.
func renderBaseline(options findingsOptions, lines []string) string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		payload, ok := findings.Baseline(line, options.label, options.pwd, options.cwd)
		if !ok {
			continue
		}
		kept = append(kept, payload)
	}
	return joinLines(kept)
}

// renderPrint applies the print transform, which already returns each finding
// with its trailing newline, and concatenates the results.
func renderPrint(options findingsOptions, lines []string) string {
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(findings.Print(line, options.pwd, options.cwd))
	}
	return builder.String()
}

// renderRanges renders the diff hunk spans as the awk file<TAB>start<TAB>end
// rows, one per line.
func renderRanges(ranges []findings.Range) string {
	rows := make([]string, 0, len(ranges))
	for _, span := range ranges {
		rows = append(rows, span.File+"\t"+strconv.Itoa(span.Start)+"\t"+strconv.Itoa(span.End))
	}
	return joinLines(rows)
}

// loadKeySet reads the saved-key file the map action filters against, storing
// each line verbatim as the awk does when it populates keyset[$0] from its first
// file argument.
func loadKeySet(path string) (map[string]struct{}, error) {
	if path == "" {
		return nil, errMissingKeyFile
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	lines, err := readLines(file)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		keys[line] = struct{}{}
	}
	return keys, nil
}

// loadRanges reads the range file the linefilter action consumes and parses each
// file<TAB>start<TAB>end row back into a span, mirroring the awk remember_range
// split on a tab and numeric coercion of the start and end fields.
func loadRanges(path string) ([]findings.Range, error) {
	if path == "" {
		return nil, errMissingRangeFile
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	lines, err := readLines(file)
	if err != nil {
		return nil, err
	}
	ranges := make([]findings.Range, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		start, _ := strconv.Atoi(fields[1])
		end, _ := strconv.Atoi(fields[2])
		ranges = append(ranges, findings.Range{File: fields[0], Start: start, End: end})
	}
	return ranges, nil
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

// readLines reads all lines from the reader, dropping the trailing newline on
// each so an N-line input yields N elements, matching how the awk reads records.
func readLines(reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lines := make([]string, 0)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// mapLines returns a new slice with transform applied to every element.
func mapLines(lines []string, transform func(string) string) []string {
	out := make([]string, len(lines))
	for index, line := range lines {
		out[index] = transform(line)
	}
	return out
}

// joinLines joins lines with newline separators and a trailing newline when the
// slice is non-empty, matching awk print which terminates every record with a
// newline. An empty slice yields the empty string so no spurious newline prints.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
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

const errMissingKeyFile sentinelError = "map action requires --keyfile"

const errMissingRangeFile sentinelError = "linefilter action requires --rangefile"

type unknownOptionError struct {
	option string
}

func (errorValue *unknownOptionError) Error() string {
	return "unknown option " + errorValue.option
}

type missingValueError struct {
	option string
}

func (errorValue *missingValueError) Error() string {
	return errorValue.option + " requires a value"
}

type unknownActionError struct {
	action string
}

func (errorValue *unknownActionError) Error() string {
	return "unknown findings action " + errorValue.action
}
