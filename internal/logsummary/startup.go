package logsummary

import "log/slog"

const (
	// ErrorKey identifies the error in a structured log event.
	ErrorKey = "err"

	invocationMessage = "go-mk invoked"
)

// RunCLI sets up logging before running the command.
//
// Capability probes write machine-readable output and skip normal logging setup.
//
//	code := RunCLI(capabilityProbe, setupLogging, run)
func RunCLI(
	capabilityProbe func() bool,
	setupLogging func(),
	run func() int,
) int {
	if !capabilityProbe() {
		setupLogging()
		slog.Debug(invocationMessage)
	}

	return run()
}
