package logsummary

import "log/slog"

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
		slog.Debug("go-mk invoked")
	}

	return run()
}
