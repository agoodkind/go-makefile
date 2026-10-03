// Mutation test orchestration for go-mk. The mutation command installs the
// pinned mutation tool, runs it once per package directory, and writes a
// combined JSON report and a Markdown summary. The pure shaping logic is in
// internal/mutation. This file owns process execution and file access; boundary
// functions emit a structured slog event.
package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"goodkind.io/go-makefile/internal/mutation"
)

// defaultMutationInstall pins the mutation tool. Consumers override the release
// through MUTATION_INSTALL.
const defaultMutationInstall = "github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0"

// defaultMutationTimeoutCoefficient multiplies the coverage run time to set the
// timeout of each mutant test run. The coverage run can replay cached test
// results in milliseconds, and each mutant run compiles the mutated package.
// The tool default timed out every mutant in that case.
const defaultMutationTimeoutCoefficient = "20"

const (
	defaultMutationReport  = makeDir + "/mutation-report.json"
	defaultMutationSummary = makeDir + "/mutation-summary.md"
)

// errMutationNoReport signals a tool run that wrote no JSON report. The tool
// output was already printed.
const errMutationNoReport sentinelError = "mutation tool wrote no report"

// runMutation runs the mutation tool over MUTATION_PACKAGES and writes the
// report and the summary. It exits 0 unless the tool fails to run or the score
// is below a positive MUTATION_MIN_SCORE.
func runMutation() int {
	if err := ensureMakeDir(); err != nil {
		return statusFromError(err)
	}
	minimum, err := mutationMinimumScore()
	if err != nil {
		writeStdout("mutation: MUTATION_MIN_SCORE must be a number from 0 to 100\n")
		return 2
	}
	installSpec := lintEnvDefault("MUTATION_INSTALL", defaultMutationInstall)
	if err := installGoTool(installSpec); err != nil {
		return statusFromError(err)
	}
	binary, err := mutationBinaryPath(installSpec)
	if err != nil {
		return statusFromError(err)
	}
	packages := splitWords(lintEnvDefault("MUTATION_PACKAGES", "."))
	reports := make([]mutation.PackageReport, 0, len(packages))
	for index, packageDir := range packages {
		report, runErr := runMutationPackage(binary, packageDir, index)
		if runErr != nil {
			return statusFromError(runErr)
		}
		reports = append(reports, mutation.PackageReport{Package: packageDir, Report: report})
	}
	summary := mutation.Summarize(reports)
	rendered := mutation.RenderMarkdown(summary, minimum)
	if err := writeMutationOutputs(summary, rendered); err != nil {
		return statusFromError(err)
	}
	writeStdout(rendered)
	if !summary.MeetsMinimum(minimum) {
		return 1
	}
	return 0
}

// mutationMinimumScore parses MUTATION_MIN_SCORE. An empty value is no minimum.
func mutationMinimumScore() (float64, error) {
	text := strings.TrimSpace(os.Getenv("MUTATION_MIN_SCORE"))
	if text == "" {
		return 0, nil
	}
	return strconv.ParseFloat(text, 64)
}

// mutationBinaryPath returns the installed tool path under GOPATH/bin for the
// install spec.
func mutationBinaryPath(installSpec string) (string, error) {
	gopath, err := goEnvPath("GOPATH")
	if err != nil {
		return "", err
	}
	binaryName := filepath.Base(strings.SplitN(installSpec, "@", 2)[0])
	return filepath.Join(gopath, "bin", binaryName), nil
}

// runMutationPackage runs the tool over one package directory and returns its
// parsed JSON report. The report file decides success: the tool can exit
// non-zero and still write a report. The function emits a boundary log before
// it removes and reads files.
func runMutationPackage(binary, packageDir string, index int) (mutation.Report, error) {
	slog.Info("mutation run package", slog.String("package", packageDir))
	reportPath := filepath.Join(makeDir, "mutation."+itoa(index)+".json")
	logPath := filepath.Join(makeDir, "mutation."+itoa(index)+".log")
	if err := os.Remove(reportPath); err != nil && !os.IsNotExist(err) {
		return mutation.Report{}, err
	}
	args := []string{
		"unleash",
		"--timeout-coefficient", lintEnvDefault("MUTATION_TIMEOUT_COEFFICIENT", defaultMutationTimeoutCoefficient),
	}
	args = append(args, splitWords(os.Getenv("MUTATION_FLAGS"))...)
	args = append(args, "--output", reportPath, packageDir)
	if _, err := captureCommand(binary, args, logPath); err != nil {
		return mutation.Report{}, err
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		if os.IsNotExist(err) {
			printMutationLog(packageDir, logPath)
			return mutation.Report{}, errMutationNoReport
		}
		return mutation.Report{}, err
	}
	return mutation.ParseReport(data)
}

// printMutationLog prints the captured tool output for a package that produced
// no report.
func printMutationLog(packageDir, logPath string) {
	writeStdout("mutation: no report for " + packageDir + "\n")
	if content, err := readFileContent(logPath); err == nil {
		writeStdout(content)
	}
}

// writeMutationOutputs writes the combined JSON report and the Markdown summary.
// It emits a boundary log before the writes.
func writeMutationOutputs(summary mutation.Summary, rendered string) error {
	reportPath := lintEnvDefault("MUTATION_REPORT", defaultMutationReport)
	summaryPath := lintEnvDefault("MUTATION_SUMMARY", defaultMutationSummary)
	slog.Info("mutation write outputs", slog.String("report", reportPath), slog.String("summary", summaryPath))
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(reportPath, append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(summaryPath, []byte(rendered), 0o644)
}
