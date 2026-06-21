package main

import (
	"strings"
	"testing"
)

// TestPlatformMatrixParsesDeclaredTargets confirms GO_MK_PLATFORMS is split into
// goos/goarch targets and that malformed entries are dropped.
func TestPlatformMatrixParsesDeclaredTargets(t *testing.T) {
	t.Setenv("GO_MK_PLATFORMS", "linux/amd64 freebsd/amd64 badentry /amd64 linux/")
	got := platformMatrix()
	want := []platformTarget{{goos: "linux", goarch: "amd64"}, {goos: "freebsd", goarch: "amd64"}}
	if len(got) != len(want) {
		t.Fatalf("platformMatrix() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("platformMatrix()[%d] = %v, want %v", index, got[index], want[index])
		}
	}
}

// TestPlatformMatrixEmptyWhenUnset confirms an unset variable yields no targets,
// which keeps the host-platform behavior for consumers that do not opt in.
func TestPlatformMatrixEmptyWhenUnset(t *testing.T) {
	t.Setenv("GO_MK_PLATFORMS", "")
	if got := platformMatrix(); len(got) != 0 {
		t.Fatalf("platformMatrix() = %v, want empty", got)
	}
}

// TestRunAcrossPlatformsRunsEachTargetAndAggregates confirms the loop runs the
// function once per declared platform with the platform forced, fails the run
// when any platform fails, and clears the active platform afterward.
func TestRunAcrossPlatformsRunsEachTargetAndAggregates(t *testing.T) {
	t.Setenv("GO_MK_PLATFORMS", "linux/amd64 freebsd/amd64")
	seen := make([]platformTarget, 0, 2)
	status := runAcrossPlatforms(func() int {
		seen = append(seen, activePlatform)
		if activePlatform.goos == "freebsd" {
			return 2
		}
		return 0
	})
	if status != 2 {
		t.Fatalf("status = %d, want 2 (a failing platform fails the run)", status)
	}
	want := []platformTarget{{goos: "linux", goarch: "amd64"}, {goos: "freebsd", goarch: "amd64"}}
	if len(seen) != len(want) || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
	if activePlatform != (platformTarget{}) {
		t.Fatalf("activePlatform = %v, want cleared after the run", activePlatform)
	}
}

// TestRunAcrossPlatformsHostOnlyWhenUnset confirms the loop runs once on the host
// and leaves the active platform empty when no matrix is declared.
func TestRunAcrossPlatformsHostOnlyWhenUnset(t *testing.T) {
	t.Setenv("GO_MK_PLATFORMS", "")
	calls := 0
	status := runAcrossPlatforms(func() int {
		calls++
		return 0
	})
	if calls != 1 || status != 0 {
		t.Fatalf("calls=%d status=%d, want 1/0", calls, status)
	}
	if activePlatform != (platformTarget{}) {
		t.Fatalf("activePlatform = %v, want empty", activePlatform)
	}
}

// TestLintEnvForcesActivePlatform confirms lintEnv overrides GOOS/GOARCH from the
// active platform, which is what scopes a gate to the target build.
func TestLintEnvForcesActivePlatform(t *testing.T) {
	activePlatform = platformTarget{goos: "freebsd", goarch: "amd64"}
	defer func() { activePlatform = platformTarget{} }()
	env := lintEnv()
	if !envContains(env, "GOOS=freebsd") {
		t.Fatalf("lintEnv did not force GOOS=freebsd: %v", goEnvEntries(env))
	}
	if !envContains(env, "GOARCH=amd64") {
		t.Fatalf("lintEnv did not force GOARCH=amd64: %v", goEnvEntries(env))
	}
}

// envContains reports whether env has the exact KEY=VALUE entry.
func envContains(env []string, entry string) bool {
	for _, current := range env {
		if current == entry {
			return true
		}
	}
	return false
}

// goEnvEntries returns the GOOS/GOARCH entries for a failure message.
func goEnvEntries(env []string) []string {
	out := make([]string, 0, 2)
	for _, current := range env {
		if strings.HasPrefix(current, "GOOS=") || strings.HasPrefix(current, "GOARCH=") {
			out = append(out, current)
		}
	}
	return out
}
