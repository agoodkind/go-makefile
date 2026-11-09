package lint

import (
	"os"
	"strings"
)

// RunTest runs Go tests with the configured targets and linker flags.
//
// The linker flags remain a single argument, including any spaces.
//
// For example, select a package with:
//
//	GO_TEST_TARGETS=./pkg make test
func RunTest(
	run func(string, []string) error,
) error {
	targets := os.Getenv("GO_TEST_TARGETS")
	if targets == "" {
		targets = "./..."
	}

	args := append([]string{"test", "-vet=off"}, testLDFlags()...)
	args = append(args, strings.Fields(targets)...)

	return run("go", args)
}

func testLDFlags() []string {
	value := strings.TrimSpace(os.Getenv("GO_TEST_LDFLAGS"))
	if value == "" {
		return nil
	}

	return []string{"-ldflags", value}
}
