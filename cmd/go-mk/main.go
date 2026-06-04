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
	"regexp"
	"strconv"
	"strings"
	"time"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/capture"
	"goodkind.io/go-makefile/internal/findings"
	"goodkind.io/go-makefile/internal/lintgate"
	"goodkind.io/go-makefile/internal/logsummary"
)

// writeStdout writes user-facing output to standard output.
func writeStdout(text string) {
	_, _ = os.Stdout.WriteString(text)
}

// writeStderr writes diagnostics to standard error.
func writeStderr(text string) {
	_, _ = os.Stderr.WriteString(text)
}

func main() {
	logsummary.Install(os.Stderr, logsummary.ParseMode(os.Getenv("GO_MK_LOG")))
	// main is the process boundary, so it emits one structured event to
	// satisfy missing_boundary_log. It is Debug so the summary handler keeps it
	// below the INFO threshold it collapses; GO_MK_LOG=debug surfaces it.
	slog.Debug("go-mk invoked")
	code := run()
	os.Exit(code)
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
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	return writeManifest(manifest, emitJSON)
}

// writeManifest plans and writes every component in the manifest, then renders
// the per-component statistics, mirroring the back half of the shell
// write-batch flow. It is shared by the write-batch subcommand (which loads the
// manifest from a file or stdin) and the baseline subcommand (which builds the
// manifest in process), so both honour the BASELINE_OUTPUT_FORMAT=json override
// and emit identical roll-up text. It writes files and stdout, so it stays in
// package main.
func writeManifest(manifest baseline.Manifest, emitJSON bool) error {
	if os.Getenv("BASELINE_OUTPUT_FORMAT") == "json" {
		emitJSON = true
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

// lintConcurrencyOptions holds the parsed flags for the lint-concurrency
// subcommand. The command layer fills it from argv and the capture package
// consumes the values, so the pure resolver never reads argv or the process
// environment. When cpu or load is left at its sentinel, the host probes in
// internal/capture supply the value.
type lintConcurrencyOptions struct {
	goflags     string
	cpu         int
	load        float64
	cpuSet      bool
	loadSet     bool
	probeName   string
	probeArgs   []string
	probeOutput string
}

// runLintConcurrency resolves the effective lint concurrency from the host (or
// the --cpu/--load overrides) and prints the resolved concurrency and the
// rewritten GOFLAGS to stdout, mirroring go_mk_resolve_lint_concurrency followed
// by go_mk_lint_goflags. When --probe is supplied it also runs that command
// through capture.Run, capturing combined output to --probe-out, and prints the
// captured exit status, so capture.Run is exercised from the command layer.
// This command layer owns stdout; the capture package stays diagnostic-only.
func runLintConcurrency(args []string) error {
	options, err := parseLintConcurrencyOptions(args)
	if err != nil {
		return err
	}

	var concurrency int
	if options.cpuSet || options.loadSet {
		cpu := options.cpu
		if !options.cpuSet {
			cpu = 1
		}
		concurrency = capture.ResolveConcurrency(cpu, options.load)
	} else {
		concurrency = capture.HostConcurrency()
	}

	goflags := options.goflags
	if goflags == "" {
		goflags = os.Getenv("GOFLAGS")
	}
	rewritten := capture.LintGOFLAGS(goflags, concurrency)

	writeStdout("concurrency: " + strconv.Itoa(concurrency) + "\n")
	writeStdout("GOFLAGS: " + rewritten + "\n")

	if options.probeName == "" {
		return nil
	}
	outputPath := options.probeOutput
	if outputPath == "" {
		return errMissingProbeOutput
	}
	result, runErr := capture.Run(options.probeName, options.probeArgs, os.Environ(), outputPath)
	if runErr != nil {
		return runErr
	}
	writeStdout("probe-status: " + strconv.Itoa(result.Status) + "\n")
	writeStdout("probe-output: " + result.OutputPath + "\n")
	return nil
}

// parseLintConcurrencyOptions parses the lint-concurrency flags from argv. The
// --cpu and --load flags record that they were set so the resolver knows to use
// the overrides instead of probing the host, and repeated --probe-arg flags
// accumulate into the probe argv.
func parseLintConcurrencyOptions(args []string) (lintConcurrencyOptions, error) {
	options := lintConcurrencyOptions{}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if index+1 >= len(args) {
			return lintConcurrencyOptions{}, &missingValueError{option: argument}
		}
		index++
		value := args[index]
		if assignErr := assignLintConcurrencyFlag(&options, argument, value); assignErr != nil {
			return lintConcurrencyOptions{}, assignErr
		}
	}
	return options, nil
}

// assignLintConcurrencyFlag stores one parsed flag value into options, parsing
// the numeric --cpu and --load values and accumulating repeated --probe-arg
// flags. It returns an error for an unknown flag or an unparseable numeric
// value, keeping the parse loop a flat dispatch rather than a bare-string switch.
func assignLintConcurrencyFlag(options *lintConcurrencyOptions, argument, value string) error {
	if argument == "--goflags" {
		options.goflags = value
		return nil
	}
	if argument == "--cpu" {
		parsed, convErr := strconv.Atoi(value)
		if convErr != nil {
			return &invalidValueError{option: argument, value: value}
		}
		options.cpu = parsed
		options.cpuSet = true
		return nil
	}
	if argument == "--load" {
		parsed, convErr := strconv.ParseFloat(value, 64)
		if convErr != nil {
			return &invalidValueError{option: argument, value: value}
		}
		options.load = parsed
		options.loadSet = true
		return nil
	}
	if argument == "--probe" {
		options.probeName = value
		return nil
	}
	if argument == "--probe-arg" {
		options.probeArgs = append(options.probeArgs, value)
		return nil
	}
	if argument == "--probe-out" {
		options.probeOutput = value
		return nil
	}
	return &unknownOptionError{option: argument}
}

// gateOptions holds the parsed flags for the gate subcommand. The command layer
// fills it from argv, reads the two findings files, and compiles the optional
// exclude and scope patterns to *regexp.Regexp, so the pure lintgate package
// never touches argv, the filesystem, or regexp compilation.
type gateOptions struct {
	name         string
	findingsPath string
	baselinePath string
	remediation  string
	exclude      *regexp.Regexp
	scope        *regexp.Regexp
}

// runGate reads the current findings file and the baseline file, applies the
// optional exclude and scope regexps, calls lintgate.Evaluate, prints the
// lintgate.Render block to stdout, and reports whether the gate passed. It
// returns false (not an error) when new findings appear so main exits non-zero
// without printing a go-mk error line, mirroring go_mk_run_baseline_diff_gate.
// This command layer owns stdout and the file reads; the lintgate package stays
// pure.
func runGate(args []string) (bool, error) {
	options, err := parseGateOptions(args)
	if err != nil {
		return false, err
	}

	currentFindings, err := readFindingsFile(options.findingsPath)
	if err != nil {
		return false, err
	}
	baselineFindings, err := readFindingsFile(options.baselinePath)
	if err != nil {
		return false, err
	}

	result := lintgate.Evaluate(
		options.name,
		currentFindings,
		baselineFindings,
		options.exclude,
		options.scope,
		options.remediation,
	)

	for _, line := range lintgate.Render(result) {
		writeStdout(line + "\n")
	}
	return result.Passed, nil
}

// parseGateOptions parses the gate flags from argv, compiling the --exclude and
// --scope patterns to regexps and leaving them nil when absent so lintgate
// treats them as the shell empty-pattern pass-through.
func parseGateOptions(args []string) (gateOptions, error) {
	options := gateOptions{}
	var excludePattern, scopePattern string
	stringTargets := map[string]*string{
		"--name":        &options.name,
		"--findings":    &options.findingsPath,
		"--baseline":    &options.baselinePath,
		"--remediation": &options.remediation,
		"--exclude":     &excludePattern,
		"--scope":       &scopePattern,
	}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		target, ok := stringTargets[argument]
		if !ok {
			return gateOptions{}, &unknownOptionError{option: argument}
		}
		if index+1 >= len(args) {
			return gateOptions{}, &missingValueError{option: argument}
		}
		index++
		*target = args[index]
	}
	if options.findingsPath == "" {
		return gateOptions{}, errMissingFindingsPath
	}
	if options.baselinePath == "" {
		return gateOptions{}, errMissingBaselinePath
	}
	if excludePattern != "" {
		compiled, compileErr := regexp.Compile(excludePattern)
		if compileErr != nil {
			return gateOptions{}, &invalidValueError{option: "--exclude", value: excludePattern}
		}
		options.exclude = compiled
	}
	if scopePattern != "" {
		compiled, compileErr := regexp.Compile(scopePattern)
		if compileErr != nil {
			return gateOptions{}, &invalidValueError{option: "--scope", value: scopePattern}
		}
		options.scope = compiled
	}
	return options, nil
}

// readFindingsFile reads a findings file into one slice element per line,
// reusing readLines so the gate sees the same N-line-to-N-element shape the
// findings subcommand uses.
func readFindingsFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return readLines(file)
}

type sentinelError string

func (errorValue sentinelError) Error() string { return string(errorValue) }

const errMissingManifestPath sentinelError = "--manifest requires a path"

const errMissingKeyFile sentinelError = "map action requires --keyfile"

const errMissingRangeFile sentinelError = "linefilter action requires --rangefile"

const errMissingProbeOutput sentinelError = "--probe requires --probe-out"

const errMissingFindingsPath sentinelError = "gate requires --findings"

const errMissingBaselinePath sentinelError = "gate requires --baseline"

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

type invalidValueError struct {
	option string
	value  string
}

func (errorValue *invalidValueError) Error() string {
	return "invalid value " + errorValue.value + " for " + errorValue.option
}
