package selfupdate

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// RunUpdateCommand runs a small flag-based update command.
func RunUpdateCommand(
	ctx context.Context,
	options Options,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	flagSet := flag.NewFlagSet("update", flag.ContinueOnError)
	flagSet.SetOutput(stderr)
	checkOnly := flagSet.Bool("check", false, "check for updates")
	dryRun := flagSet.Bool("dry-run", false, "stage and verify without replacing")
	if err := flagSet.Parse(args); err != nil {
		return 1
	}
	if flagSet.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected argument: %s\n", flagSet.Arg(0))
		return 1
	}
	if *checkOnly {
		result, err := Check(ctx, options)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		printCheckResult(stdout, result)
		return 0
	}
	options.DryRun = options.DryRun || *dryRun
	result, err := Apply(ctx, options)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	printApplyResult(stdout, result)
	return 0
}

func printCheckResult(stdout io.Writer, result CheckResult) {
	fmt.Fprintf(stdout, "current: %s\n", result.CurrentVersion)
	fmt.Fprintf(stdout, "latest: %s\n", result.LatestTag)
	fmt.Fprintf(stdout, "available: %t\n", result.UpdateAvailable)
}

func printApplyResult(stdout io.Writer, result ApplyResult) {
	printCheckResult(stdout, result.CheckResult)
	fmt.Fprintf(stdout, "applied: %t\n", result.Applied)
	fmt.Fprintf(stdout, "dry_run: %t\n", result.DryRun)
}
