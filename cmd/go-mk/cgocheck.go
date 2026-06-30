// cgo-stub check for go-mk. A binary that imports a non-stdlib cgo package but
// is built with CGO_ENABLED=0 either fails to compile or, worse, links a stub
// that builds clean and fails at runtime. github.com/mattn/go-sqlite3 is the
// motivating case: with cgo disabled it registers the driver and then errors on
// the first real call ("requires cgo to work. This is a stub"), so the release
// ships a silently broken binary. This check fails the build instead, and is a
// no-op whenever cgo is enabled.
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
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		wrapped := fmt.Errorf("cgo-stub: go list failed: %w: %s", err, strings.TrimSpace(out.String()))
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
