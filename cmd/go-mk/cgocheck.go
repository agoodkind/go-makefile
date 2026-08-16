// cgo-stub check for go-mk. A binary that imports a non-stdlib cgo package but
// is built with CGO_ENABLED=0 either fails to compile or, worse, links a stub
// that builds clean and fails at runtime. github.com/mattn/go-sqlite3 is the
// motivating case: with cgo disabled it registers the driver and then errors on
// the first real call ("requires cgo to work. This is a stub"), so the release
// ships a silently broken binary. This check fails the build instead, and is a
// no-op whenever cgo is enabled.
//
// The release stages judge cgo per (binary, platform) pair through
// checkReleaseCgoStub rather than the single ambient checkCgoStub below,
// because a release can carry more than one GOOS/GOARCH target and more than
// one binary, and a RELEASE_BINS entry can force cgo on for one binary alone
// (releaseBinary.cgo in release.go) without changing the ambient CGO_ENABLED.
// The build-check entry point (runCgoStubCheckStep) has no release config to
// read, so it keeps using checkCgoStub and the ambient CGO_ENABLED unchanged.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"goodkind.io/go-makefile/internal/report"
)

// cgoListPackage is the subset of `go list -json` output the check reads.
type cgoListPackage struct {
	ImportPath string
	Standard   bool
	CgoFiles   []string
}

// cgoDisabled reports whether the build runs with cgo off. Go enables cgo by
// default when CGO_ENABLED is unset, so only an explicit "0" disables it.
func cgoDisabled() bool {
	return strings.TrimSpace(os.Getenv("CGO_ENABLED")) == "0"
}

// cgoOptionalAllowlist parses GO_MK_CGO_OPTIONAL into a set of import paths the
// consumer asserts are safe to build with cgo disabled, for the rare package
// that ships a working pure-Go fallback rather than a stub.
func cgoOptionalAllowlist() map[string]bool {
	allow := map[string]bool{}
	for _, importPath := range strings.Fields(os.Getenv("GO_MK_CGO_OPTIONAL")) {
		allow[importPath] = true
	}
	return allow
}

// cgoStubTargets returns the packages whose build graph the check inspects: the
// release main package when CMD is set, otherwise the whole module.
func cgoStubTargets() []string {
	if cmd := strings.TrimSpace(os.Getenv("CMD")); cmd != "" {
		return []string{cmd}
	}
	return []string{"./..."}
}

// filterCgoRequiringPackages returns the non-stdlib packages that carry cgo
// files and are not allowlisted. These are the packages that stub or break when
// the binary is built with cgo disabled. Standard-library cgo (net, os/user)
// is excluded because it has a real pure-Go fallback.
func filterCgoRequiringPackages(packages []cgoListPackage, allow map[string]bool) []string {
	found := make([]string, 0, len(packages))
	for _, pkg := range packages {
		if pkg.Standard || len(pkg.CgoFiles) == 0 {
			continue
		}
		if allow[pkg.ImportPath] {
			continue
		}
		found = append(found, pkg.ImportPath)
	}
	return found
}

// cgoRequiringPackages lists the build graph of targets and returns the
// non-stdlib cgo packages in it. It lists with CGO_ENABLED=1 so cgo files are
// visible regardless of the ambient setting, and -e so a partial graph still
// reports.
func cgoRequiringPackages(targets []string) ([]string, error) {
	slog.Info("cgo-stub run go list", slog.Int("targets", len(targets)))
	args := append([]string{"list", "-e", "-deps", "-json"}, targets...)
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	// Keep stdout and stderr separate: `go list` writes the JSON package stream
	// to stdout, but "go: downloading ..." progress and any diagnostics go to
	// stderr. Merging them corrupts the JSON decode when the module cache is cold
	// (a fresh release runner), so decode stdout alone and report stderr only on
	// failure.
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		wrapped := fmt.Errorf("cgo-stub: go list failed: %w: %s", err, strings.TrimSpace(errOut.String()))
		slog.Error("cgo-stub go list failed", slog.Any("err", wrapped))
		return nil, wrapped
	}
	packages, err := decodeGoListPackages(&out)
	if err != nil {
		return nil, err
	}
	return filterCgoRequiringPackages(packages, cgoOptionalAllowlist()), nil
}

// decodeGoListPackages decodes the stream of JSON objects that `go list -json`
// writes. The output is concatenated top-level objects, not an array, and a
// json.Decoder ranges over that stream with More: each Decode reads one object
// and More reports whether another object follows. It returns the decoded
// packages or a wrapped error on malformed output.
func decodeGoListPackages(reader io.Reader) ([]cgoListPackage, error) {
	packages := make([]cgoListPackage, 0, 64)
	decoder := json.NewDecoder(reader)
	for decoder.More() {
		var pkg cgoListPackage
		if err := decoder.Decode(&pkg); err != nil {
			wrapped := fmt.Errorf("cgo-stub: decode go list output: %w", err)
			slog.Error("cgo-stub decode failed", slog.Any("err", wrapped))
			return nil, wrapped
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

// checkCgoStub fails when the build is configured with cgo disabled while the
// build graph imports non-stdlib cgo packages. It is a no-op when cgo is on.
func checkCgoStub() error {
	if !cgoDisabled() {
		return nil
	}
	packages, err := cgoRequiringPackages(cgoStubTargets())
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return nil
	}
	return fmt.Errorf(
		"CGO_ENABLED=0 but the build graph requires cgo: %s. "+
			"Building these with cgo disabled ships a stubbed or non-functional binary. "+
			"Enable cgo (pass cgo: true to the release workflow, or set CGO_ENABLED=1), "+
			"or list a package in GO_MK_CGO_OPTIONAL when its pure-Go fallback is intended.",
		strings.Join(packages, ", "),
	)
}

// runCgoStubCheckStep wraps checkCgoStub as a captured build-check step.
func runCgoStubCheckStep() (report.StepResult, int) {
	if err := checkCgoStub(); err != nil {
		return report.StepResult{Name: "cgo-stub", Status: report.StatusFailed, Findings: splitOutputLines(err.Error())}, 1
	}
	return report.StepResult{Name: "cgo-stub", Status: report.StatusOK}, 0
}

// releaseCgoEnabled reports whether one release binary builds with cgo enabled:
// either the binary's own RELEASE_BINS cgo option forces it on, or the ambient
// CGO_ENABLED the build environment resolves is not "0". It reads
// cgoEnabledValue rather than cgoDisabled because a release build defaults
// CGO_ENABLED to "0" when unset (see cgoEnabledValue in release.go, which
// buildPlatformEnv applies to the real `go build`), unlike the build-check path
// cgoDisabled assumes cgo stays on when CGO_ENABLED is unset.
func releaseCgoEnabled(binary releaseBinary) bool {
	return binary.cgo || cgoEnabledValue() != "0"
}

// releaseCgoRequiringPackages lists the non-stdlib cgo packages in one release
// binary's own build graph for one release platform. It lists with
// CGO_ENABLED=1 and GOOS/GOARCH forced to the target through platformListEnv,
// the same override checkPlatformStub uses, so a runner that cross-compiles the
// target (a freebsd release built on a linux runner) is judged against the
// target's graph rather than the runner host's.
func releaseCgoRequiringPackages(mainPkg, goos, goarch string) ([]string, error) {
	slog.Info("cgo-stub run go list for release target",
		slog.String("pkg", mainPkg), slog.String("goos", goos), slog.String("goarch", goarch))
	args := []string{"list", "-e", "-deps", "-json", mainPkg}
	cmd := exec.Command("go", args...)
	cmd.Env = platformListEnv(os.Environ(), goos, goarch)
	// Keep stdout and stderr separate for the same reason cgoRequiringPackages
	// does: a cold module cache writes "go: downloading ..." progress to stderr,
	// which would corrupt the JSON decode if merged into stdout.
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		wrapped := fmt.Errorf("cgo-stub: go list %s for %s/%s failed: %w: %s",
			mainPkg, goos, goarch, err, strings.TrimSpace(errOut.String()))
		slog.Error("cgo-stub go list failed", slog.Any("err", wrapped))
		return nil, wrapped
	}
	packages, err := decodeGoListPackages(&out)
	if err != nil {
		return nil, err
	}
	return filterCgoRequiringPackages(packages, cgoOptionalAllowlist()), nil
}

// checkReleaseCgoStub fails when a release binary builds with cgo effectively
// disabled for a platform it targets while its own build graph, for that
// platform, requires cgo. It replaces the single ambient checkCgoStub call the
// release stages made before the platform loop: that call read only the
// ambient CGO_ENABLED, so it never saw a RELEASE_BINS binary's own cgo option,
// and it never forced GOOS/GOARCH, so it always judged the runner host's graph
// rather than the platform actually being compiled, which is why a freebsd
// compile job running on a linux runner was failed for a package that is not
// in the freebsd build at all. A pair whose cgo is effectively enabled is
// skipped, exactly as checkCgoStub is a no-op when cgo is on.
func checkReleaseCgoStub(cfg releaseConfig) error {
	findings := make([]string, 0)
	for _, platform := range cfg.platforms {
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok {
			return fmt.Errorf("cgo-stub: malformed platform %q (want os/arch)", platform)
		}
		for _, binary := range releaseBinaries(cfg) {
			if !binary.buildsFor(platform) {
				continue
			}
			if releaseCgoEnabled(binary) {
				continue
			}
			packages, err := releaseCgoRequiringPackages(binary.mainPkg, goos, goarch)
			if err != nil {
				return err
			}
			if len(packages) == 0 {
				continue
			}
			findings = append(findings, fmt.Sprintf("%s (%s): %s", binary.name, platform, strings.Join(packages, ", ")))
		}
	}
	if len(findings) == 0 {
		return nil
	}
	return fmt.Errorf(
		"CGO_ENABLED=0 but the build graph requires cgo: %s. "+
			"Building these with cgo disabled ships a stubbed or non-functional binary. "+
			"Enable cgo for the binary (a RELEASE_BINS cgo=1 option), for the platform (CGO_ENABLED=1), "+
			"or for the whole release (pass cgo: true to the release workflow), "+
			"or list a package in GO_MK_CGO_OPTIONAL when its pure-Go fallback is intended.",
		strings.Join(findings, "; "),
	)
}
