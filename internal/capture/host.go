// Host-environment probes for capture: the CPU count and the 1-minute load
// average. These mirror the processor_count and load_average reads in
// go_mk_resolve_lint_concurrency, preferring /proc/loadavg on Linux and
// sysctl -n vm.loadavg on Darwin/BSD, and routing read failures through slog.
package capture

import (
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// HostConcurrency resolves the effective lint concurrency from the host CPU
// count and 1-minute load average, mirroring go_mk_run_lint_cpu's auto path.
func HostConcurrency() int {
	return ResolveConcurrency(hostCPUCount(), hostLoadAverage())
}

// hostCPUCount returns the usable CPU count with a floor of 1, mirroring the
// shell's processor_count guard.
func hostCPUCount() int {
	count := runtime.NumCPU()
	if count < 1 {
		return 1
	}
	return count
}

// hostLoadAverage reads the 1-minute load average, preferring /proc/loadavg on
// Linux and falling back to sysctl -n vm.loadavg on Darwin/BSD. It returns 0
// when no source is readable, mirroring the shell's load_average=0 default.
func hostLoadAverage() float64 {
	raw, readErr := os.ReadFile("/proc/loadavg")
	if readErr == nil {
		fields := strings.Fields(string(raw))
		if len(fields) > 0 {
			value, parseErr := strconv.ParseFloat(fields[0], 64)
			if parseErr == nil {
				return value
			}
		}
		slog.Warn("capture.hostLoadAverage could not parse /proc/loadavg",
			"raw", string(raw),
		)
	}

	out, execErr := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if execErr != nil {
		slog.Warn("capture.hostLoadAverage sysctl vm.loadavg failed, defaulting to 0",
			"error", execErr,
		)
		return 0
	}

	// sysctl prints "{ N.NN N.NN N.NN }"; the 1-minute average is field index 1.
	fields := strings.Fields(string(out))
	if len(fields) > 1 {
		value, parseErr := strconv.ParseFloat(fields[1], 64)
		if parseErr == nil {
			return value
		}
	}

	slog.Warn("capture.hostLoadAverage could not parse sysctl vm.loadavg, defaulting to 0",
		"raw", string(out),
	)
	return 0
}
