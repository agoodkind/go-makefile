package cigate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"goodkind.io/go-makefile/internal/lint"
)

func Run(
	cacheManifest func() int,
	modulePrefix func() string,
	defaultLintGates string,
	now func() time.Time,
) int {
	layout := layoutMode(os.Getenv("GO_MK_JOB_LAYOUT"))
	if layout == qualityGateLayout {
		if err := writeGateManifest(
			cacheManifest,
			modulePrefix,
			defaultLintGates,
			now(),
		); err != nil {
			fmt.Fprintln(os.Stderr, "go-mk-quality-gate-manifest:", err)
			return 1
		}

		return 0
	}

	if err := writeJobLayout(layout); err != nil {
		fmt.Fprintln(os.Stderr, "go-mk-ci-job-layout:", err)
		return 1
	}

	return 0
}

func writeGateManifest(
	cacheManifest func() int,
	modulePrefix func() string,
	defaultLintGates string,
	now time.Time,
) error {
	target := os.Getenv("GO_MK_CI_QUALITY_GATE")
	if target == "" {
		return fmt.Errorf("set GO_MK_CI_QUALITY_GATE to a quality gate target")
	}

	gate, err := gateForTarget(target)
	if err != nil {
		return err
	}

	restoreCgo := gate.usesCgo && os.Getenv("GO_MK_CGO_TOOLCHAIN_ID") != ""
	restoreLint := gate.restoresLintCache(defaultLintGates)
	key, prefix := "", ""
	if restoreLint {
		key, prefix, err = lintCacheKeys(modulePrefix(), now)
		if err != nil {
			return err
		}
	}

	if restoreCgo || restoreLint {
		if status := cacheManifest(); status != 0 {
			return fmt.Errorf(
				"read go-makefile cache paths: cache manifest exited with status %d",
				status,
			)
		}
	}

	return appendOutputs(
		fmt.Sprintf(
			"quality_gate=%s\nquality_gate_command=%s\nrestore_cgo=%t\nlint_cache_key=%s\nlint_cache_restore_key=%s\n",
			target,
			gate.Command,
			restoreCgo,
			key,
			prefix,
		),
	)
}

func (gate entry) restoresLintCache(defaultLintGates string) bool {
	if gate.GolangciLintCacheSlug == "" {
		return false
	}

	if gate.Command != buildCheckCommand {
		return true
	}

	gates := strings.Fields(os.Getenv("LINT_GATES"))
	if len(gates) == 0 {
		gates = strings.Fields(defaultLintGates)
	}

	return slices.Contains(gates, golangciGate)
}

func lintCacheKeys(modulePrefix string, now time.Time) (string, string, error) {
	linter, err := lint.GolangciInstallSpec()
	if err != nil {
		return "", "", err
	}

	dependencies, err := lintDependencyHash()
	if err != nil {
		return "", "", err
	}

	scope := sha256.Sum256([]byte(modulePrefix + "\x00" + linter))
	prefix := fmt.Sprintf("go-mk-golangci-%s-%s-%x-", runtime.GOOS, runtime.GOARCH, scope)
	year, week := now.UTC().ISOWeek()

	return fmt.Sprintf("%s%s-%04d-%02d", prefix, dependencies, year, week), prefix, nil
}

func lintDependencyHash() (string, error) {
	var manifest strings.Builder
	for _, name := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
		data, err := os.ReadFile(name)
		if name != "go.mod" && errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return "", err
		}

		fmt.Fprintf(&manifest, "%s %x\n", name, sha256.Sum256(data))
	}

	return fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String()))), nil
}
