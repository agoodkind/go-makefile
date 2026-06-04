// CLI wiring for go-mk. The command tree is built with cobra and executed
// through charm.land/fang when stdout is a terminal, which styles help and
// errors; off a terminal the plain cobra executor runs so piped and
// machine-parsed output (the -flags probe and the findings streams) stay free
// of styling. Each leaf command calls the same handler the engine has always
// used, so behavior and exit codes are unchanged; only the dispatch layer
// moves from a hand-rolled switch to cobra.
package main

import (
	"context"
	"os"

	fang "charm.land/fang/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// recordedExit carries the handler exit code out of a cobra RunE. The handlers
// return process codes and print their own diagnostics, so RunE records the
// code and returns nil; fang and cobra then see success and never render an
// error box over the handler's own output. A genuine cobra error (an unknown
// command or a malformed flag) is handled separately in run.
var recordedExit int

// run builds the command tree, answers the -flags capability probe directly,
// then executes through fang on a terminal or plain cobra otherwise.
func run() int {
	root := newRootCommand()
	if len(os.Args) >= 2 && (os.Args[1] == "-flags" || os.Args[1] == "--flags") {
		printCapabilities(root)
		return 0
	}
	recordedExit = 0
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if err := fang.Execute(context.Background(), root); err != nil {
			// fang already rendered the error to stderr.
			return 2
		}
		return recordedExit
	}
	if err := root.ExecuteContext(context.Background()); err != nil {
		writeStderr("go-mk: " + err.Error() + "\n")
		return 2
	}
	return recordedExit
}

// printCapabilities prints one "Name: <command>" line per registered command.
// The shell resolver greps this to detect a binary that predates a capability,
// so it is generated from the live command tree and stays in sync with it.
func printCapabilities(root *cobra.Command) {
	for _, command := range root.Commands() {
		writeStdout("Name: " + command.Name() + "\n")
	}
}

// newRootCommand assembles the go-mk command tree. Registration is split by
// handler shape so each helper stays small.
func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "go-mk",
		Short:         "The go-makefile engine: lint, baseline, build, install, and release.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			_ = command.Help()
			recordedExit = 2
			return nil
		},
	}
	registerLintCommands(root)
	registerEngineCommands(root)
	registerInputCommands(root)
	return root
}

// namedCommand pairs a command name with its one-line help.
type namedCommand struct {
	use   string
	short string
}

// codeCommand is a no-argument command that returns a process exit code.
type codeCommand struct {
	use   string
	short string
	run   func() int
}

// lintNoArgCommands are the no-argument gates the runLint dispatcher owns. They
// route back through runLint so the lint command table stays in one place.
var lintNoArgCommands = []namedCommand{
	{"lint", "Run the lint chain over every gate in LINT_GATES"},
	{"lint-tools", "Install golangci-lint, gofumpt, and goimports"},
	{"lint-golangci", "Run golangci-lint with the baseline gate"},
	{"lint-golangci-scope", "Run one golangci linter or rule against its baseline slice"},
	{"lint-format", "Run the formatter diff gate"},
	{"lint-gocyclo", "Run gocyclo with the baseline gate"},
	{"lint-deadcode", "Run deadcode with the baseline gate"},
	{"lint-files", "Run scoped lint against LINT_FILES"},
	{"lint-diff", "Run scoped lint against staged Go files and lines"},
	{"fmt", "Apply the configured formatters"},
	{"vet", "Run go vet"},
	{"test", "Run go test"},
	{"govulncheck", "Run govulncheck"},
	{"staticcheck-extra", "Run the custom staticcheck-extra analyzer with the baseline gate"},
	{"staticcheck-extra-bin", "Resolve and build the staticcheck-extra analyzer binary"},
}

// lintArgCommands are the argument-taking capture gates the runLint dispatcher
// owns.
var lintArgCommands = []namedCommand{
	{"capture-golangci", "Capture golangci findings to files"},
	{"capture-golangci-baseline", "Capture golangci baseline findings to files"},
	{"capture-golangci-scope", "Capture one golangci linter or rule to files"},
	{"capture-gocyclo", "Capture gocyclo findings to files"},
	{"capture-deadcode", "Capture deadcode findings to files"},
	{"staticcheck-extra-capture", "Capture staticcheck-extra findings to files"},
}

// registerLintCommands adds the lint and capture gates, each routed through the
// runLint dispatcher.
func registerLintCommands(root *cobra.Command) {
	for _, entry := range lintNoArgCommands {
		name := entry.use
		root.AddCommand(&cobra.Command{
			Use:   entry.use,
			Short: entry.short,
			Args:  cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				recordedExit, _ = runLint(name, nil)
				return nil
			},
		})
	}
	for _, entry := range lintArgCommands {
		name := entry.use
		root.AddCommand(&cobra.Command{
			Use:                entry.use,
			Short:              entry.short,
			DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, args []string) error {
				recordedExit, _ = runLint(name, args)
				return nil
			},
		})
	}
}

// registerEngineCommands adds the no-argument build, install, release, and
// reporting commands that return a code directly.
func registerEngineCommands(root *cobra.Command) {
	commands := []codeCommand{
		{"notice", "Print pending one-time pipeline notices", runNotice},
		{"build-check", "Run the full non-test quality gate: vet, lint, and govulncheck", runBuildCheck},
		{"release", "Cross-compile, sign, archive, and publish a GitHub release", runRelease},
		{"build", "Build every declared binary into the dist directory", runBuild},
		{"install", "Build and install every declared binary", runInstall},
		{"uninstall", "Remove every declared binary from its install directory", runUninstall},
		{"go-version-check", "Report whether go.mod tracks the latest Go release", runGoVersionCheck},
	}
	for _, entry := range commands {
		handler := entry.run
		root.AddCommand(&cobra.Command{
			Use:   entry.use,
			Short: entry.short,
			Args:  cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				recordedExit = handler()
				return nil
			},
		})
	}
}

// registerInputCommands adds the commands that consume raw arguments or stdin:
// the baseline rewriters, the findings transformer, the concurrency resolver,
// and the gate. Flag parsing is disabled so each handler keeps its own
// argument parsing, which the make layer drives with fixed argument strings.
func registerInputCommands(root *cobra.Command) {
	root.AddCommand(passThroughCommand("write-batch", "Rewrite every baseline in a manifest", errToCode(runWriteBatch)))
	root.AddCommand(passThroughCommand("findings", "Transform finding lines read from stdin", errToCode(runFindings)))
	root.AddCommand(passThroughCommand("lint-concurrency", "Resolve lint concurrency and GOFLAGS from the host", errToCode(runLintConcurrency)))
	root.AddCommand(passThroughCommand("baseline-gate", "Diff findings against a baseline and confirm the result", runGateConfirm))
	root.AddCommand(passThroughCommand("baseline", "Rewrite the lint baselines", runBaseline))
	root.AddCommand(passThroughCommand("gate", "Diff current findings against a baseline", gateToCode))
}

// passThroughCommand builds a flag-parsing-disabled command whose RunE records
// the code the handler returns for the raw arguments.
func passThroughCommand(use, short string, handler func([]string) int) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			recordedExit = handler(args)
			return nil
		},
	}
}

// errToCode adapts an error-returning handler to the code shape, printing the
// engine's diagnostic line and returning 1 on error.
func errToCode(handler func([]string) error) func([]string) int {
	return func(args []string) int {
		if err := handler(args); err != nil {
			writeStderr("go-mk: " + err.Error() + "\n")
			return 1
		}
		return 0
	}
}

// gateToCode adapts the gate handler: a real error prints a diagnostic and
// exits 1, while new findings exit 1 with no extra line because the gate block
// was already printed.
func gateToCode(args []string) int {
	passed, err := runGate(args)
	if err != nil {
		writeStderr("go-mk: " + err.Error() + "\n")
		return 1
	}
	if !passed {
		return 1
	}
	return 0
}
