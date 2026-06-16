package main

// buildGateConfig keeps the CI proof and local gate runner injectable so tests
// can prove the decision without spawning the whole lint toolchain.
type buildGateConfig struct {
	proofValid func() bool
	buildCheck func() int
	stdout     func(string)
}

// runBuildGate is the single build chokepoint used by make fallback builds and
// the go-mk build/install engine paths.
func runBuildGate() int {
	return runBuildGateWith(buildGateConfig{
		proofValid: validCIBuildProof,
		buildCheck: runBuildCheck,
		stdout:     writeStdout,
	})
}

// runBuildGateWith runs build-check unless a signed GitHub Actions OIDC token
// proves the separate reusable-workflow gate job is responsible for it.
func runBuildGateWith(config buildGateConfig) int {
	if config.proofValid() {
		config.stdout("build-gate: GitHub Actions OIDC proof verified; inline gates are covered by the CI gate job\n")
		return 0
	}
	return config.buildCheck()
}
