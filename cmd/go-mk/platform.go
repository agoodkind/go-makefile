// Platform matrix for go-mk. A consumer that ships more than one GOOS/GOARCH
// (for example a daemon built for linux/amd64 and freebsd/amd64) declares the
// set in GO_MK_PLATFORMS, and the analysis gates and build run once per target
// with GOOS/GOARCH forced for that pass. The host platform is the default when
// the variable is empty, so consumers that do not set it are unaffected.
//
// The matrix lives in the shared engine, not in the CI workflow, so local and CI
// run the same passes from one committed declaration. It is forced through
// lintEnv, so running a single gate, unsetting GOOS, or setting GOOS=darwin
// still runs the full matrix; the only way to narrow it is editing the committed
// Makefile.
package main

import (
	"os"
	"strings"
)

// platformTarget is one GOOS/GOARCH pair the gates and build run under.
type platformTarget struct {
	goos   string
	goarch string
}

// label renders the "goos/goarch" form used in report step names and cache
// directory suffixes.
func (p platformTarget) label() string {
	return p.goos + "/" + p.goarch
}

// activePlatform is the GOOS/GOARCH lintEnv forces for the current pass. It is
// empty outside a matrix pass, so lintEnv leaves the host values untouched. The
// check orchestration and the per-gate dispatch both run sequentially, so a
// single package-level value is safe.
var activePlatform platformTarget

// platformMatrix parses GO_MK_PLATFORMS into the declared targets. Each entry is
// "goos/goarch" and whitespace separates entries. An empty or unset variable, or
// a malformed entry, yields no targets, which keeps the host-platform behavior.
func platformMatrix() []platformTarget {
	entries := splitWords(os.Getenv("GO_MK_PLATFORMS"))
	targets := make([]platformTarget, 0, len(entries))
	for _, entry := range entries {
		goos, goarch, ok := strings.Cut(entry, "/")
		if !ok || goos == "" || goarch == "" {
			continue
		}
		targets = append(targets, platformTarget{goos: goos, goarch: goarch})
	}
	return targets
}

// runAcrossPlatforms runs fn once per declared platform with GOOS/GOARCH forced
// for that pass, aggregating the exit codes so any failing platform fails the
// run. With no matrix declared it runs fn once on the host. It clears the active
// platform after each pass so a later host-only step is unaffected.
func runAcrossPlatforms(fn func() int) int {
	targets := platformMatrix()
	if len(targets) == 0 {
		return fn()
	}
	status := 0
	for _, target := range targets {
		activePlatform = target
		code := fn()
		activePlatform = platformTarget{}
		if code != 0 {
			status = code
		}
	}
	return status
}
