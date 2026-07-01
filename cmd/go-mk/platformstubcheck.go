// platform-stub check for go-mk. A first-party package that compiles a cgo
// implementation on some release platforms and a pure-Go sibling on others ships
// a silently non-functional feature wherever the cgo build is tagged out. The
// motivating case is an engine gated `//go:build darwin && arm64` with a
// `//go:build !(darwin && arm64)` sibling whose methods return an
// "unsupported platform" error: the linux release builds clean and the feature
// is dead at runtime. This is the GOOS/GOARCH analogue of the cgo-stub check,
// which only catches the CGO_ENABLED=0 case. It is scoped to the consumer's own
// packages (`./...`, never the dependency graph), matching the other lint gates,
// so a dependency's legitimate per-platform implementation is never flagged.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"

	"goodkind.io/go-makefile/internal/report"
)

// platformListPackage is the subset of `go list -json` the check reads. GoFiles
// plus CgoFiles tell whether a package is present on a platform and whether that
// presence is a cgo build.
type platformListPackage struct {
	ImportPath string
	Standard   bool
	GoFiles    []string
	CgoFiles   []string
}

// platformCgoPresence records, across the compared platforms, whether a package
// is present as a cgo build on at least one and as a non-cgo build on at least
// one other. Both true is the platform-stub pattern.
type platformCgoPresence struct {
	withCgo    bool
	withoutCgo bool
}

// platformStubPlatforms are the os/arch targets the check compares, taken from
// the default release matrix. Comparing all of them catches a cgo build gated to
// a single os/arch (for example darwin/arm64 only).
func platformStubPlatforms() []string {
	return strings.Fields(defaultReleasePlatforms)
}

// platformStubAllowlist parses GO_MK_PLATFORM_STUB_OPTIONAL into a set of
// first-party import paths a consumer asserts are a deliberate platform split.
func platformStubAllowlist() map[string]bool {
	allow := map[string]bool{}
	for _, importPath := range strings.Fields(os.Getenv("GO_MK_PLATFORM_STUB_OPTIONAL")) {
		allow[importPath] = true
	}
	return allow
}

// platformListEnv returns base with CGO_ENABLED, GOOS, and GOARCH overridden for
// the target. It replaces any existing entry rather than appending, so a
// caller-set or matrix-inherited GOOS/GOARCH in the parent environment cannot
// win and make every list resolve for the same platform, which would hide a real
// split.
func platformListEnv(base []string, goos, goarch string) []string {
	env := setEnvVar(base, "CGO_ENABLED", "1")
	env = setEnvVar(env, "GOOS", goos)
	env = setEnvVar(env, "GOARCH", goarch)
	return env
}

// firstPartyPackages lists the consumer's own packages for one os/arch with cgo
// enabled so cgo files are visible, using ./... so the dependency graph is never
// read, and -e so a package that fails to build still reports its files.
func firstPartyPackages(goos, goarch string) ([]platformListPackage, error) {
	slog.Info("platform-stub run go list", slog.String("goos", goos), slog.String("goarch", goarch))
	cmd := exec.Command("go", "list", "-e", "-json", "./...")
	cmd.Env = platformListEnv(os.Environ(), goos, goarch)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		wrapped := fmt.Errorf("platform-stub: go list %s/%s failed: %w: %s", goos, goarch, err, strings.TrimSpace(errBuf.String()))
		slog.Error("platform-stub go list failed", slog.Any("err", wrapped))
		return nil, wrapped
	}
	return decodePlatformPackages(&out)
}

// decodePlatformPackages decodes the concatenated JSON objects `go list -json`
// writes, the same stream shape the cgo-stub check reads.
func decodePlatformPackages(reader *bytes.Buffer) ([]platformListPackage, error) {
	packages := make([]platformListPackage, 0, 64)
	decoder := json.NewDecoder(reader)
	for decoder.More() {
		var pkg platformListPackage
		if err := decoder.Decode(&pkg); err != nil {
			wrapped := fmt.Errorf("platform-stub: decode go list output: %w", err)
			slog.Error("platform-stub decode failed", slog.Any("err", wrapped))
			return nil, wrapped
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

// recordPlatformPresence folds one platform's package listing into the running
// presence map. A package with no matched files is absent on that platform and
// contributes nothing; a package built with cgo files sets withCgo, otherwise
// withoutCgo.
func recordPlatformPresence(presence map[string]*platformCgoPresence, packages []platformListPackage) {
	for _, pkg := range packages {
		if pkg.Standard {
			continue
		}
		if len(pkg.GoFiles)+len(pkg.CgoFiles) == 0 {
			continue
		}
		state := presence[pkg.ImportPath]
		if state == nil {
			state = &platformCgoPresence{}
			presence[pkg.ImportPath] = state
		}
		if len(pkg.CgoFiles) > 0 {
			state.withCgo = true
		} else {
			state.withoutCgo = true
		}
	}
}

// flagPlatformSplitPackages returns the sorted import paths that are present as a
// cgo build on some platform and a non-cgo build on another, minus the
// allowlist. That split is the silent platform-stub pattern.
func flagPlatformSplitPackages(presence map[string]*platformCgoPresence, allow map[string]bool) []string {
	flagged := make([]string, 0, len(presence))
	for importPath, state := range presence {
		if allow[importPath] {
			continue
		}
		if state.withCgo && state.withoutCgo {
			flagged = append(flagged, importPath)
		}
	}
	sort.Strings(flagged)
	return flagged
}

// checkPlatformStub fails when a first-party package compiles cgo on some release
// platforms and a pure-Go sibling on others.
func checkPlatformStub() error {
	platforms := platformStubPlatforms()
	if len(platforms) < 2 {
		return nil
	}
	presence := map[string]*platformCgoPresence{}
	for _, platform := range platforms {
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok {
			return fmt.Errorf("platform-stub: malformed platform %q (want os/arch)", platform)
		}
		packages, err := firstPartyPackages(goos, goarch)
		if err != nil {
			return err
		}
		recordPlatformPresence(presence, packages)
	}
	flagged := flagPlatformSplitPackages(presence, platformStubAllowlist())
	if len(flagged) == 0 {
		return nil
	}
	return fmt.Errorf(
		"first-party package(s) compile cgo on some release platforms and a pure-Go sibling on others: %s. "+
			"Every platform without the cgo build gets a silently non-functional implementation. "+
			"Provide a real implementation for every release platform, "+
			"or list the package in GO_MK_PLATFORM_STUB_OPTIONAL when the split is deliberate.",
		strings.Join(flagged, ", "),
	)
}

// runPlatformStubCheckStep wraps checkPlatformStub as a captured build-check step.
func runPlatformStubCheckStep() (report.StepResult, int) {
	if err := checkPlatformStub(); err != nil {
		return report.StepResult{Name: "platform-stub", Status: report.StatusFailed, Findings: splitOutputLines(err.Error())}, 1
	}
	return report.StepResult{Name: "platform-stub", Status: report.StatusOK}, 0
}
