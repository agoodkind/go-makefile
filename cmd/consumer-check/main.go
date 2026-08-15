// consumer-check runs a real consumer build against two go-makefile revisions
// and reports what happened. A change to this repository reaches every
// consumer's next build, so the only honest test of one is a consumer build
// that actually runs. Unit tests in this repository cannot see a name that a
// consumer resolves but this repository never resolves.
//
// For each consumer and each revision it clones the consumer, exports the
// revision into a scratch directory, points GO_MK_DEV_DIR at that export, and
// runs the requested make target. It records the exit code, the wall time, and
// the full log per pair.
//
// It reports; it does not judge. A consumer exits non-zero for its own lint
// findings as readily as for a broken pipeline, and no rule this tool could
// apply tells those apart reliably. Reading the logs is the verdict step, and
// it belongs to a person. The table exists to say which logs are worth reading
// and how long each build took.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// checkOptions is the resolved input: which consumers to build, which
// go-makefile revisions to compare, and what to run in each consumer.
type checkOptions struct {
	engineRepo string
	revisions  []string
	consumers  []string
	target     string
	timeout    time.Duration
	workDir    string
	removeWork bool
	inPlace    bool
	depth      int
}

// buildResult is one consumer built against one revision.
type buildResult struct {
	consumer string
	revision string
	exitCode int
	elapsed  time.Duration
	logPath  string
	skipped  string
}

// comparison is one consumer's results across every revision, reduced to
// whether the revisions agreed.
type comparison struct {
	consumer string
	results  []buildResult
	agree    bool
}

const (
	defaultTarget  = "build"
	defaultTimeout = 20 * time.Minute
	exitUsage      = 2
	exitDisagree   = 1
)

var (
	errNoConsumers       = errors.New("no consumer paths given")
	errConsumersDisagree = errors.New("consumers behave differently across the revisions")
)

func main() {
	slog.Info("consumer-check invoked")
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseOptions(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "consumer-check: %v\n", err)
		return exitUsage
	}

	// A run leaves a clone per consumer per revision plus every log, which is
	// gigabytes across a fleet. Keep them only when asked, and only remove a
	// directory this process created.
	if options.removeWork {
		defer func() {
			if removeErr := os.RemoveAll(options.workDir); removeErr != nil {
				slog.WarnContext(ctx, "consumer-check could not remove its scratch directory",
					slog.String("path", options.workDir), slog.String("err", removeErr.Error()))
			}
		}()
	} else {
		fmt.Fprintf(stdout, "scratch directory: %s\n", options.workDir)
	}

	exports, err := exportRevisions(ctx, options)
	if err != nil {
		fmt.Fprintf(stderr, "consumer-check: %v\n", err)
		return exitUsage
	}

	comparisons := make([]comparison, 0, len(options.consumers))
	for _, consumer := range options.consumers {
		results := make([]buildResult, 0, len(options.revisions))
		for index, revision := range options.revisions {
			result := buildOne(ctx, options, consumer, revision, index, exports[revision])
			reportOne(stdout, result)
			results = append(results, result)
		}
		comparisons = append(comparisons, comparison{
			consumer: filepath.Base(consumer),
			results:  results,
			agree:    resultsAgree(results),
		})
	}

	return reportSummary(stdout, comparisons)
}

func parseOptions(args []string, stderr io.Writer) (checkOptions, error) {
	set := flag.NewFlagSet("consumer-check", flag.ContinueOnError)
	set.SetOutput(stderr)

	var (
		engineRepo = set.String("engine", ".", "go-makefile repository to export revisions from")
		revisions  = set.String("revisions", "", "comma-separated revisions to compare, for example origin/main,HEAD")
		target     = set.String("target", defaultTarget, "make target to run in each consumer")
		timeout    = set.Duration("timeout", defaultTimeout, "per-build timeout")
		workDir    = set.String("work", "", "scratch directory (a temporary one is used when empty)")
		keepWork   = set.Bool("keep", false, "keep the scratch directory after the run")
		depth      = set.Int("depth", 1, "clone depth; 0 clones full history")
		inPlace    = set.Bool("in-place", false,
			"build in the given path instead of cloning, which writes to that checkout")
	)
	if err := set.Parse(args); err != nil {
		return checkOptions{}, err
	}

	consumers := set.Args()
	if len(consumers) == 0 {
		return checkOptions{}, errNoConsumers
	}

	resolved := make([]string, 0, len(consumers))
	for _, consumer := range consumers {
		absolute, err := filepath.Abs(consumer)
		if err != nil {
			slog.Error("consumer-check could not resolve a consumer path",
				slog.String("path", consumer), slog.String("err", err.Error()))
			return checkOptions{}, fmt.Errorf("resolve %s: %w", consumer, err)
		}
		resolved = append(resolved, absolute)
	}

	revisionList := splitList(*revisions)
	if len(revisionList) < 2 {
		return checkOptions{}, errors.New("give at least two revisions to compare")
	}

	engineAbsolute, err := filepath.Abs(*engineRepo)
	if err != nil {
		slog.Error("consumer-check could not resolve the engine repository",
			slog.String("path", *engineRepo), slog.String("err", err.Error()))
		return checkOptions{}, fmt.Errorf("resolve engine repo: %w", err)
	}

	// The scratch path becomes GO_MK_DEV_DIR, and a build runs with its working
	// directory inside the clone, so a relative path would resolve against the
	// clone and the consumer would not find the engine.
	work := *workDir
	removeWork := false
	if work == "" {
		created, mkErr := os.MkdirTemp("", "consumer-check-")
		if mkErr != nil {
			slog.Error("consumer-check could not create a scratch directory",
				slog.String("err", mkErr.Error()))
			return checkOptions{}, fmt.Errorf("create scratch directory: %w", mkErr)
		}
		work = created
		removeWork = !*keepWork
	}
	workAbsolute, err := filepath.Abs(work)
	if err != nil {
		slog.Error("consumer-check could not resolve the scratch directory",
			slog.String("path", work), slog.String("err", err.Error()))
		return checkOptions{}, fmt.Errorf("resolve scratch directory: %w", err)
	}

	return checkOptions{
		engineRepo: engineAbsolute,
		revisions:  revisionList,
		consumers:  resolved,
		target:     *target,
		timeout:    *timeout,
		workDir:    workAbsolute,
		removeWork: removeWork,
		inPlace:    *inPlace,
		depth:      *depth,
	}, nil
}

func splitList(text string) []string {
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// exportRevisions writes each revision's tree into its own directory, so a
// build points GO_MK_DEV_DIR at exactly that revision and nothing else.
//
// The directory name carries the revision's position as well as its sanitized
// text. Sanitizing alone is not one-to-one: release/v1 and release-v1 produce
// the same name, the second export would land on top of the first, and both
// revisions would then build against one tree while appearing to compare two.
// Each directory is also cleared first, so a leftover file from an earlier run
// cannot join the export.
func exportRevisions(ctx context.Context, options checkOptions) (map[string]string, error) {
	exports := make(map[string]string, len(options.revisions))
	for index, revision := range options.revisions {
		name := fmt.Sprintf("%02d-%s", index, sanitize(revision))
		destination := filepath.Join(options.workDir, "engine", name)
		if err := os.RemoveAll(destination); err != nil {
			slog.ErrorContext(ctx, "consumer-check could not clear an export directory",
				slog.String("path", destination), slog.String("err", err.Error()))
			return nil, fmt.Errorf("clear %s: %w", destination, err)
		}
		if err := os.MkdirAll(destination, 0o755); err != nil {
			slog.ErrorContext(ctx, "consumer-check could not create an export directory",
				slog.String("path", destination), slog.String("err", err.Error()))
			return nil, fmt.Errorf("create %s: %w", destination, err)
		}
		if err := exportTree(ctx, options.engineRepo, revision, destination); err != nil {
			slog.ErrorContext(ctx, "consumer-check could not export a revision",
				slog.String("revision", revision), slog.String("err", err.Error()))
			return nil, fmt.Errorf("export %s: %w", revision, err)
		}
		exports[revision] = destination
	}
	slog.InfoContext(ctx, "consumer-check exported revisions",
		slog.Int("count", len(exports)),
		slog.String("engine_repo", options.engineRepo))
	return exports, nil
}

// exportTree writes one revision's tree into destination. It is a process
// boundary: git streams the archive into tar.
func exportTree(ctx context.Context, repo string, revision string, destination string) error {
	slog.InfoContext(ctx, "consumer-check exporting revision",
		slog.String("revision", revision), slog.String("destination", destination))
	archive := exec.CommandContext(ctx, "git", "-C", repo, "archive", revision)
	extract := exec.CommandContext(ctx, "tar", "-x", "-C", destination)

	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	extract.Stdin = pipe

	if err := archive.Start(); err != nil {
		return err
	}
	if err := extract.Start(); err != nil {
		return err
	}
	if err := archive.Wait(); err != nil {
		return err
	}
	return extract.Wait()
}

// buildOne clones the consumer and runs the target against one revision. The
// clone keeps .git, because several consumers run git during their own build
// for version stamping or submodule content.
func buildOne(ctx context.Context, options checkOptions, consumer string, revision string, index int, engine string) buildResult {
	name := filepath.Base(consumer)
	result := buildResult{consumer: name, revision: revision}
	// The revision's position joins its sanitized text for the same reason the
	// export directories carry one: sanitizing alone can map two revisions to
	// one name, and one clone or log would then stand for both.
	slot := fmt.Sprintf("%s-%02d-%s", name, index, sanitize(revision))

	// A consumer can be a subdirectory of a larger repository, so the clone
	// covers the whole repository and the build runs in the matching subpath.
	repoRoot, subPath, rootErr := repositoryRoot(ctx, consumer)
	if rootErr != nil {
		result.skipped = rootErr.Error()
		return result
	}

	buildDir := consumer
	if !options.inPlace {
		clone := filepath.Join(options.workDir, slot)
		if err := prepareClone(ctx, repoRoot, clone, options.depth); err != nil {
			result.skipped = err.Error()
			return result
		}
		buildDir = filepath.Join(clone, subPath)
	}

	logPath := filepath.Join(options.workDir, slot+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		result.skipped = fmt.Sprintf("create log: %v", err)
		return result
	}
	defer func() { _ = logFile.Close() }()
	result.logPath = logPath

	buildContext, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()

	command := exec.CommandContext(buildContext, "make", options.target)
	command.Dir = buildDir
	command.Env = buildEnvironment(engine)
	command.Stdout = logFile
	command.Stderr = logFile

	start := time.Now()
	runErr := command.Run()
	result.elapsed = time.Since(start)
	result.exitCode = exitCodeOf(runErr)

	slog.InfoContext(ctx, "consumer-check built consumer",
		slog.String("consumer", name),
		slog.String("revision", revision),
		slog.Int("exit_code", result.exitCode),
		slog.Duration("elapsed", result.elapsed))
	return result
}

// repositoryRoot resolves the git repository holding the consumer and the
// consumer's path within it. Several consumers live in a subdirectory of a
// larger repository, so the clone has to cover the repository while the build
// runs in the subdirectory.
func repositoryRoot(ctx context.Context, consumer string) (string, string, error) {
	top, topErr := gitOutput(ctx, consumer, "rev-parse", "--show-toplevel")
	if topErr != nil {
		slog.ErrorContext(ctx, "consumer-check could not resolve a repository root",
			slog.String("consumer", consumer), slog.String("err", topErr.Error()))
		return "", "", fmt.Errorf("repository root for %s: %w", consumer, topErr)
	}
	prefix, prefixErr := gitOutput(ctx, consumer, "rev-parse", "--show-prefix")
	if prefixErr != nil {
		slog.ErrorContext(ctx, "consumer-check could not resolve a repository prefix",
			slog.String("consumer", consumer), slog.String("err", prefixErr.Error()))
		return "", "", fmt.Errorf("repository prefix for %s: %w", consumer, prefixErr)
	}
	return top, filepath.FromSlash(strings.TrimSuffix(prefix, "/")), nil
}

// gitOutput runs one git query and returns its trimmed stdout. It is a process
// boundary, so a failure is logged here rather than only at the caller.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		slog.ErrorContext(ctx, "consumer-check git query failed",
			slog.String("dir", dir),
			slog.String("args", strings.Join(args, " ")),
			slog.String("err", err.Error()))
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// prepareClone makes a throwaway copy of the consumer. The copy keeps .git,
// because several consumers run git during their own build for version stamping
// or submodule content. A shallow clone needs the file:// form, since git treats
// a plain local path as a hardlink copy and ignores --depth there.
func prepareClone(ctx context.Context, source string, destination string, depth int) error {
	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("clear %s: %w", destination, err)
	}

	args := []string{"clone", "--quiet", "--recurse-submodules"}
	origin := source
	if depth > 0 {
		args = append(args, "--depth", fmt.Sprint(depth), "--shallow-submodules")
		origin = "file://" + source
	} else {
		args = append(args, "--local", "--no-hardlinks")
	}
	args = append(args, origin, destination)

	command := exec.CommandContext(ctx, "git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		slog.ErrorContext(ctx, "consumer-check clone failed",
			slog.String("source", source),
			slog.String("output", strings.TrimSpace(string(output))),
			slog.String("err", err.Error()))
		return fmt.Errorf("clone %s: %w: %s", source, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// buildEnvironment drops the make variables an outer make exports, so the
// consumer resolves its own settings rather than inheriting this process's.
func buildEnvironment(engine string) []string {
	inherited := []string{"MAKEFLAGS", "MFLAGS", "MAKELEVEL", "GO_MK_", "GOLANGCI_"}
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		skip := false
		for _, prefix := range inherited {
			if strings.HasPrefix(name, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			environment = append(environment, entry)
		}
	}
	return append(environment, "GO_MK_DEV_DIR="+engine)
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func resultsAgree(results []buildResult) bool {
	if len(results) == 0 {
		return true
	}
	// A consumer skipped on every revision was never measured, which is not the
	// same as measuring two different outcomes. It is reported as unmeasured.
	first := results[0]
	for _, result := range results[1:] {
		if (result.skipped != "") != (first.skipped != "") {
			return false
		}
		if result.skipped == "" && result.exitCode != first.exitCode {
			return false
		}
	}
	return true
}

// wasMeasured reports whether any revision actually ran the target.
func wasMeasured(results []buildResult) bool {
	for _, result := range results {
		if result.skipped == "" {
			return true
		}
	}
	return false
}

func sanitize(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, text)
}

func reportOne(stdout io.Writer, result buildResult) {
	if result.skipped != "" {
		fmt.Fprintf(stdout, "%-22s %-16s SKIP  %s\n", result.consumer, result.revision, result.skipped)
		return
	}
	status := "ok"
	if result.exitCode != 0 {
		status = fmt.Sprintf("exit %d", result.exitCode)
	}
	fmt.Fprintf(stdout, "%-22s %-16s %-8s %6.1fs  %s\n",
		result.consumer, result.revision, status, result.elapsed.Seconds(), result.logPath)
}

// reportSummary prints the exit code and wall time per revision, and marks the
// consumers whose exit codes differ.
//
// Matching exit codes are not a verdict. A consumer exits non-zero for its own
// lint findings as readily as for a broken pipeline, and this tool cannot tell
// those apart. Read the logs to decide which happened; the paths are printed
// above for that reason.
func reportSummary(stdout io.Writer, comparisons []comparison) int {
	sort.Slice(comparisons, func(i int, j int) bool {
		return comparisons[i].consumer < comparisons[j].consumer
	})

	disagreements := 0
	unmeasured := 0
	fmt.Fprintf(stdout, "\n%-22s %-10s %s\n", "consumer", "exit code", "per revision")
	for _, item := range comparisons {
		verdict := "same"
		switch {
		case !wasMeasured(item.results):
			verdict = "unmeasured"
			unmeasured++
		case !item.agree:
			verdict = "DIFFERS"
			disagreements++
		}
		parts := make([]string, 0, len(item.results))
		for _, result := range item.results {
			if result.skipped != "" {
				parts = append(parts, result.revision+"=skipped")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=exit%d/%.0fs",
				result.revision, result.exitCode, result.elapsed.Seconds()))
		}
		fmt.Fprintf(stdout, "%-22s %-10s %s\n", item.consumer, verdict, strings.Join(parts, "  "))
	}

	fmt.Fprintf(stdout, "\nRead the logs above before drawing a conclusion. "+
		"A non-zero exit is as likely to be the consumer's own findings as a broken pipeline.\n")

	if unmeasured > 0 {
		fmt.Fprintf(stdout, "%d consumer(s) were never measured; see the skip reason above\n", unmeasured)
	}
	if disagreements > 0 {
		fmt.Fprintf(stdout, "%d consumer(s) exited differently across the revisions\n", disagreements)
		slog.Error("consumer-check found differing exit codes",
			slog.Int("count", disagreements),
			slog.String("err", errConsumersDisagree.Error()))
		return exitDisagree
	}
	fmt.Fprintf(stdout, "every consumer exited the same on both revisions\n")
	return 0
}
