// Package capture ports the lint-concurrency and command-capture helpers from
// scripts/go-mk-common.sh into pure-ish Go. ResolveConcurrency and LintGOFLAGS
// are deterministic and reproduce the shell arithmetic and GOFLAGS tokenization
// exactly. Run owns the one impure boundary: it spawns an external process and
// writes its combined output to a file, emitting a structured slog call at that
// boundary. The package never writes to stdout/stderr directly; diagnostics
// route through slog, mirroring the internal/baseline and internal/findings
// split.
//
// =============================================================================
// capture
// =============================================================================
package capture

import (
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ResolveConcurrency reproduces go_mk_resolve_lint_concurrency's awk arithmetic.
// The shell BEGIN block (scripts/go-mk-common.sh lines 390-404) computes:
//
//	value = int(processor_count - load_average - 1)
//	minimum = processor_count < 2 ? 1 : 2
//	if (value < minimum) { value = minimum }
//	if (value > processor_count) { value = processor_count }
//	print value
//
// awk's int() truncates toward zero, which Go's int(float64) conversion matches
// for the non-negative load averages this sees. The result is clamped up to a
// floor (1 for a single CPU, otherwise 2) and down to the CPU count.
func ResolveConcurrency(cpuCount int, loadAverage float64) int {
	value := int(float64(cpuCount) - loadAverage - 1.0)

	minimum := 2
	if cpuCount < 2 {
		minimum = 1
	}
	if value < minimum {
		value = minimum
	}
	if value > cpuCount {
		value = cpuCount
	}

	return value
}

// LintGOFLAGS reproduces go_mk_lint_goflags: split the existing GOFLAGS on
// whitespace (matching the shell's unquoted word-splitting), drop any -p=...
// token, then append -p=<concurrency>. When no tokens survive, the result is
// just the -p= token, matching the shell's empty-output_flags branch.
func LintGOFLAGS(existing string, concurrency int) string {
	var kept []string
	for _, token := range strings.Fields(existing) {
		if strings.HasPrefix(token, "-p=") {
			continue
		}
		kept = append(kept, token)
	}

	concurrencyFlag := "-p=" + strconv.Itoa(concurrency)
	if len(kept) == 0 {
		return concurrencyFlag
	}
	return strings.Join(kept, " ") + " " + concurrencyFlag
}

// Result captures the outcome of a Run: the combined-output file path and the
// command's exit status. Status mirrors GO_MK_COMMAND_STATUS in the shell
// helpers, where a non-zero command exit is recorded rather than treated as a
// hard error.
type Result struct {
	OutputPath string
	Status     int
}

// Run executes name with args and env, writing combined stdout+stderr to
// outputPath, and records the command's exit status in Result.Status. This
// mirrors go_mk_run_capture: a non-zero exit is captured in the Result, not
// returned as a Go error. An error is returned only for real failures such as
// exec-not-found or an output-file write error. A structured slog call is
// emitted at the boundary to satisfy the missing_boundary_log rule.
func Run(name string, args []string, env []string, outputPath string) (Result, error) {
	slog.Info("capture.Run executing command",
		"command", name,
		"args", args,
		"output_path", outputPath,
	)

	out, createErr := os.Create(outputPath)
	if createErr != nil {
		slog.Error("capture.Run failed to create output file",
			"output_path", outputPath,
			"error", createErr,
		)
		return Result{OutputPath: outputPath}, createErr
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			slog.Error("capture.Run failed to close output file",
				"output_path", outputPath,
				"error", closeErr,
			)
		}
	}()

	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = out

	runErr := cmd.Run()
	if runErr == nil {
		return Result{OutputPath: outputPath, Status: 0}, nil
	}

	exitErr, isExit := runErr.(*exec.ExitError)
	if isExit {
		slog.Info("capture.Run command exited non-zero",
			"command", name,
			"status", exitErr.ExitCode(),
			"output_path", outputPath,
		)
		return Result{OutputPath: outputPath, Status: exitErr.ExitCode()}, nil
	}

	slog.Error("capture.Run failed to execute command",
		"command", name,
		"output_path", outputPath,
		"error", runErr,
	)
	return Result{OutputPath: outputPath}, runErr
}
