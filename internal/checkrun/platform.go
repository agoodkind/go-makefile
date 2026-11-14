package checkrun

import "goodkind.io/go-makefile/internal/report"

// Plan combines vet, lint gates, and the remaining default checks.
//
// Vulnerability scanning runs only through its standalone target.
//
// Example:
//
//	checks := Plan(vet, gates, version, cgo, platform)
func Plan(
	vet []Check,
	gates []Check,
	version func() (report.StepResult, int),
	cgo func() (report.StepResult, int),
	platform func() (report.StepResult, int),
) []Check {
	checks := vet
	checks = append(checks, gates...)
	checks = append(checks, Check{Name: "go-version", Run: version})
	checks = append(checks, Check{Name: "cgo-stub", Run: cgo})
	checks = append(checks, Check{Name: "platform-stub", Run: platform})

	return checks
}

// Vet creates one vet check for each declared platform.
//
// An empty platform list creates a single host check.
//
// Example:
//
//	checks := Vet(targets, runVet, platformStep)
func Vet[Target comparable](
	targets []Target,
	runner func() (report.StepResult, int),
	platformStep func(string, func() (report.StepResult, int), Target) Check,
) []Check {
	if len(targets) == 0 {
		return []Check{{Name: "vet", Run: runner}}
	}

	checks := make([]Check, 0, len(targets))
	for _, target := range targets {
		checks = append(checks, platformStep("vet", runner, target))
	}

	return checks
}

// Gates creates the configured lint checks in their declared order.
//
// Each gate runs on every declared platform or once on the host.
//
// Example:
//
//	checks := Gates(runners, names, targets, runGate, unknown, platformGate)
func Gates[Target comparable](
	runners map[string]func() int,
	names []string,
	targets []Target,
	runGate func(string, func() int) (report.StepResult, int),
	unknown func(string) report.StepResult,
	platformGate func(string, func() int, Target) Check,
) []Check {
	checks := make([]Check, 0, len(names))
	for _, name := range names {
		runner, ok := runners[name]
		if !ok {
			checks = append(
				checks,
				Check{
					Name: name,
					Run: func() (report.StepResult, int) {
						return unknown(name), 1
					},
				},
			)

			continue
		}

		if len(targets) == 0 {
			checks = append(
				checks,
				Check{
					Name: name,
					Run: func() (report.StepResult, int) {
						return runGate(name, runner)
					},
				},
			)

			continue
		}

		for _, target := range targets {
			checks = append(checks, platformGate(name, runner, target))
		}
	}

	return checks
}

// PlatformGate creates a lint check that selects and clears its platform state.
//
// The result uses the platform label in the progress report.
//
// Example:
//
//	check := PlatformGate(name, runner, target, label, active, runGate)
func PlatformGate[Target comparable](
	name string,
	runner func() int,
	target Target,
	label string,
	active *Target,
	runGate func(string, func() int) (report.StepResult, int),
) Check {
	displayName := name + " (" + label + ")"

	return Check{
		Name: displayName,
		Run: func() (report.StepResult, int) {
			*active = target
			result, code := runGate(name, runner)
			var empty Target
			*active = empty
			result.Name = displayName

			return result, code
		},
	}
}

// PlatformStep creates a tool check that selects and clears its platform state.
//
// The result uses the platform label in the progress report.
//
// Example:
//
//	check := PlatformStep(name, runner, target, label, active)
func PlatformStep[Target comparable](
	name string,
	runner func() (report.StepResult, int),
	target Target,
	label string,
	active *Target,
) Check {
	displayName := name + " (" + label + ")"

	return Check{
		Name: displayName,
		Run: func() (report.StepResult, int) {
			*active = target
			result, code := runner()
			var empty Target
			*active = empty
			result.Name = displayName

			return result, code
		},
	}
}
