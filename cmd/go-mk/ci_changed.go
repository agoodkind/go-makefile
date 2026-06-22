// ci-changed reports whether a CI push touched anything the Go build or tests
// depend on, so the reusable workflow can skip the gate work on an irrelevant
// push without ever skipping the matrix jobs that publish required checks. The
// answer comes from the Go toolchain (go list -e) plus build-config, submodule,
// workspace, and declared codegen-input paths, never from a path glob, and
// every uncertain case fails safe to "changed" so a wrong answer over-runs
// rather than wrongly skips. This file owns the git/go process boundary and the
// GITHUB_OUTPUT write; the decision itself is the pure decideChanged.
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// zeroSHA is the all-zero before-commit GitHub sends for the first push to a new
// branch, where there is no prior commit to diff against.
const zeroSHA = "0000000000000000000000000000000000000000"

// ciChangedConfig injects the git, go, and environment seams so the decision is
// unit-testable without spawning git or the Go toolchain.
type ciChangedConfig struct {
	eventName      string
	base           string
	head           string
	workspaceUse   string
	generate       string
	generateInputs string
	outputPath     string
	prefix         string
	baseInHistory  func(base string) bool
	diffNames      func(base, head string) ([]string, error)
	sourceFiles    func() ([]string, error)
	submoduleDirs  func() ([]string, error)
	stdout         func(string)
}

// ciChangeInputs holds everything the pure decision needs, all as repo-root
// relative slash paths.
type ciChangeInputs struct {
	changedPaths  []string
	sourceFiles   []string
	submoduleDirs []string
	workspaceDirs []string
	generateDirs  []string
}

// goListPackage is the subset of `go list -json` fields that name files which
// compose the build or tests. Embed and cgo files are included so a go:embed
// payload or a C source counts as a Go change by construction.
type goListPackage struct {
	Dir             string
	Standard        bool
	GoFiles         []string
	CgoFiles        []string
	CFiles          []string
	CXXFiles        []string
	MFiles          []string
	HFiles          []string
	FFiles          []string
	SFiles          []string
	SwigFiles       []string
	SwigCXXFiles    []string
	SysoFiles       []string
	EmbedFiles      []string
	TestGoFiles     []string
	XTestGoFiles    []string
	TestEmbedFiles  []string
	XTestEmbedFiles []string
}

// buildConfigBasenames are file basenames whose change alters how the gates run.
// They are matched anywhere in the tree, not just at one module root, so a change
// to a second module's go.mod (for example staticcheck/go.mod in a repo that
// gates two modules) still runs the gates rather than being silently skipped.
var buildConfigBasenames = map[string]struct{}{
	"go.mod":                          {},
	"go.sum":                          {},
	"go.work":                         {},
	"go.work.sum":                     {},
	"Makefile":                        {},
	"bootstrap.mk":                    {},
	"golangci.yml":                    {},
	".gitmodules":                     {},
	".golangci-lint-baseline.txt":     {},
	".gocyclo-baseline.txt":           {},
	".deadcode-baseline.txt":          {},
	".staticcheck-extra-baseline.txt": {},
}

// sourceExtensions catch source files even when go list cannot see them, such as
// a deleted file (gone from the package) or a brand-new one. They mirror the
// language file groups go list reports (cgo C/C++, Objective-C, Fortran, SWIG,
// assembly, and prebuilt .syso objects).
var sourceExtensions = map[string]struct{}{
	".go":      {},
	".c":       {},
	".h":       {},
	".cc":      {},
	".cpp":     {},
	".cxx":     {},
	".hpp":     {},
	".hxx":     {},
	".m":       {},
	".f":       {},
	".for":     {},
	".f90":     {},
	".s":       {},
	".swig":    {},
	".swigcxx": {},
	".syso":    {},
}

// runCIChanged resolves the git context and environment, then delegates to
// runCIChangedWith. A missing repo root means the diff cannot be scoped, so it
// runs every gate.
func runCIChanged() int {
	outputPath := strings.TrimSpace(os.Getenv("GITHUB_OUTPUT"))
	repoRoot, rootErr := loggedGitOutput("ci-changed git", "rev-parse", "--show-toplevel")
	if rootErr != nil {
		slog.Warn("ci-changed could not resolve repo root; running all gates", slog.String("error", rootErr.Error()))
		return emitChanged(outputPath, writeStdout, true, "git rev-parse failed; running all gates")
	}
	prefixRaw, _ := loggedGitOutput("ci-changed git", "rev-parse", "--show-prefix")
	prefix := filepath.ToSlash(prefixRaw)

	head := strings.TrimSpace(os.Getenv("GO_MK_DIFF_HEAD"))
	if head == "" {
		head = "HEAD"
	}

	config := ciChangedConfig{
		eventName:      strings.TrimSpace(os.Getenv("GO_MK_EVENT_NAME")),
		base:           strings.TrimSpace(os.Getenv("GO_MK_DIFF_BASE")),
		head:           head,
		workspaceUse:   os.Getenv("GO_MK_WORKSPACE_USE"),
		generate:       strings.TrimSpace(os.Getenv("GO_MK_GENERATE")),
		generateInputs: os.Getenv("GO_MK_GENERATE_INPUTS"),
		outputPath:     outputPath,
		prefix:         prefix,
		baseInHistory:  func(base string) bool { return gitIsAncestor(base, head) },
		diffNames:      gitDiffNames,
		sourceFiles:    func() ([]string, error) { return goListSourceFiles(repoRoot) },
		submoduleDirs:  func() ([]string, error) { return gitSubmoduleDirs(repoRoot) },
		stdout:         writeStdout,
	}
	return runCIChangedWith(config)
}

// runCIChangedWith applies the fail-safe gates, computes the changed paths and
// the build-input set, and emits the boolean. It always returns 0: detection is
// not a gate, and a non-zero exit would let the changes job fail and skip the
// matrix.
func runCIChangedWith(config ciChangedConfig) int {
	if config.eventName != "push" {
		return emitChanged(config.outputPath, config.stdout, true,
			"event "+config.eventName+" is not push; running all gates")
	}
	if config.base == "" || config.base == zeroSHA {
		return emitChanged(config.outputPath, config.stdout, true,
			"no base commit (new branch); running all gates")
	}
	if !config.baseInHistory(config.base) {
		return emitChanged(config.outputPath, config.stdout, true,
			"base commit "+config.base+" is not an ancestor of head (force push or shallow clone); running all gates")
	}
	changedPaths, diffErr := config.diffNames(config.base, config.head)
	if diffErr != nil {
		slog.Warn("ci-changed git diff failed; running all gates", slog.String("error", diffErr.Error()))
		return emitChanged(config.outputPath, config.stdout, true, "git diff failed; running all gates")
	}
	if len(changedPaths) == 0 {
		return emitChanged(config.outputPath, config.stdout, false, "diff is empty; nothing to gate")
	}
	generateDirs := generateInputDirs(config.generateInputs)
	if config.generate != "" && len(generateDirs) == 0 {
		return emitChanged(config.outputPath, config.stdout, true,
			"codegen declares no inputs; running all gates")
	}
	source, listErr := config.sourceFiles()
	if listErr != nil {
		slog.Warn("ci-changed go list failed; running all gates", slog.String("error", listErr.Error()))
		return emitChanged(config.outputPath, config.stdout, true, "go list failed; running all gates")
	}
	submodules, subErr := config.submoduleDirs()
	if subErr != nil {
		slog.Warn("ci-changed submodule discovery failed; running all gates", slog.String("error", subErr.Error()))
		return emitChanged(config.outputPath, config.stdout, true, "submodule discovery failed; running all gates")
	}
	changed, reason := decideChanged(ciChangeInputs{
		changedPaths:  changedPaths,
		sourceFiles:   source,
		submoduleDirs: submodules,
		workspaceDirs: workspaceTriggerDirs(config.workspaceUse, config.prefix),
		generateDirs:  generateDirs,
	})
	return emitChanged(config.outputPath, config.stdout, changed, reason)
}

// decideChanged returns whether any changed path is a Go build input. It stops
// at the first relevant path and reports it, so the summary names the trigger.
//
// A codegen repo can declare repo-root input directories with
// GO_MK_GENERATE_INPUTS so a docs-only push still skips cheaply while any change
// beneath those dirs forces the gates. A codegen repo that sets GO_MK_GENERATE
// but not GO_MK_GENERATE_INPUTS never reaches this function: the caller fails
// safe to changed=true instead.
func decideChanged(inputs ciChangeInputs) (bool, string) {
	source := make(map[string]struct{}, len(inputs.sourceFiles))
	for _, file := range inputs.sourceFiles {
		source[file] = struct{}{}
	}
	for _, path := range inputs.changedPaths {
		if _, ok := source[path]; ok {
			return true, "Go build input changed: " + path
		}
		if hasSourceExtension(path) {
			return true, "source file changed: " + path
		}
		if isBuildConfigPath(path) {
			return true, "build configuration changed: " + path
		}
		if underAnyDir(path, inputs.submoduleDirs) {
			return true, "submodule changed: " + path
		}
		if underAnyDir(path, inputs.workspaceDirs) {
			return true, "workspace input changed: " + path
		}
		if underAnyDir(path, inputs.generateDirs) {
			return true, "declared codegen input changed: " + path
		}
	}
	return false, "no Go-relevant changes"
}

// hasSourceExtension reports whether a path is a Go or cgo source file by suffix.
func hasSourceExtension(path string) bool {
	_, ok := sourceExtensions[strings.ToLower(filepath.Ext(path))]
	return ok
}

// isBuildConfigPath reports whether a changed path is a build-config file. It
// matches on the basename anywhere in the tree, plus any .mk fragment and the CI
// workflow directory, so a manifest change in any module runs the gates rather
// than being missed.
func isBuildConfigPath(path string) bool {
	if _, ok := buildConfigBasenames[filepath.Base(path)]; ok {
		return true
	}
	if strings.HasSuffix(path, ".mk") {
		return true
	}
	return strings.HasPrefix(path, ".github/workflows/")
}

// underAnyDir reports whether a path is one of the directories or sits beneath
// one of them.
func underAnyDir(path string, dirs []string) bool {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if path == dir || strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

// workspaceTriggerDirs converts GO_MK_WORKSPACE_USE into repo-root directories
// that should trigger gates. The "." entry is dropped because the module itself
// is already covered by go list and build-config, leaving the vendored modules
// whose inputs go list cannot see on their own.
func workspaceTriggerDirs(workspaceUse, prefix string) []string {
	base := strings.TrimSuffix(prefix, "/")
	dirs := make([]string, 0)
	for _, field := range strings.Fields(workspaceUse) {
		if field == "." || field == "" {
			continue
		}
		joined := field
		if base != "" {
			joined = base + "/" + field
		}
		dirs = append(dirs, filepath.ToSlash(filepath.Clean(joined)))
	}
	return dirs
}

// generateInputDirs converts GO_MK_GENERATE_INPUTS into repo-root directories
// that should trigger gates. The entries are already repo-root relative, so the
// parser only splits fields, drops ".", and normalizes separators.
func generateInputDirs(generateInputs string) []string {
	dirs := make([]string, 0)
	for _, field := range strings.Fields(generateInputs) {
		if field == "." || field == "" {
			continue
		}
		dirs = append(dirs, filepath.ToSlash(filepath.Clean(field)))
	}
	return dirs
}

// emitChanged prints a one-line human summary and, under GitHub Actions, appends
// changed=<bool> to GITHUB_OUTPUT so the calling step exposes it. It always
// returns 0.
func emitChanged(outputPath string, stdout func(string), changed bool, reason string) int {
	value := "false"
	if changed {
		value = "true"
	}
	stdout("ci-changed: " + value + " (" + reason + ")\n")
	if outputPath != "" {
		if err := appendOutput(outputPath, "changed", value); err != nil {
			slog.Warn("ci-changed could not write GITHUB_OUTPUT", slog.String("error", err.Error()))
		}
	}
	return 0
}

// appendOutput appends a key=value line to a GitHub Actions output file. It
// mutates the filesystem, so it emits a boundary log.
func appendOutput(path, key, value string) error {
	slog.Info("ci-changed write github output", slog.String("key", key), slog.String("value", value))
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.WriteString(key + "=" + value + "\n")
	return err
}

// gitIsAncestor reports whether base is an ancestor of head, which also requires
// base to exist locally. This is stricter than mere existence: a force push that
// rewrote history (base no longer reachable from head) or a shallow clone (base
// absent) makes it false, where the caller fails safe to running every gate.
func gitIsAncestor(base, head string) bool {
	slog.Info("ci-changed verify base ancestry", slog.String("base", base), slog.String("head", head))
	return exec.Command("git", "merge-base", "--is-ancestor", base, head).Run() == nil
}

// gitDiffNames returns the repo-root-relative paths changed between base and
// head, including renames and deletions. It spawns a process, so it emits a
// boundary log.
func gitDiffNames(base, head string) ([]string, error) {
	out, err := loggedGitOutput("ci-changed git diff", "diff", "--name-only", "--diff-filter=ACMRTD", base, head)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

// gitSubmoduleDirs reads the submodule paths from .gitmodules at the repo root.
// A repo with no .gitmodules, or one with no path entries, returns an empty
// slice. A genuine read failure (malformed or unreadable .gitmodules) returns an
// error so the caller fails safe rather than silently dropping submodule paths.
// It spawns a process when the file exists, so it emits a boundary log.
func gitSubmoduleDirs(root string) ([]string, error) {
	gitmodules := filepath.Join(root, ".gitmodules")
	if _, err := os.Stat(gitmodules); err != nil {
		return nil, nil
	}
	out, err := loggedGitOutput("ci-changed git submodules", "config", "--file", gitmodules, "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		// git config exits 1 when the regexp matches nothing, which is a normal
		// empty result, not a failure. Any other exit is a real read error.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	dirs := make([]string, 0)
	for _, line := range splitNonEmptyLines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			dirs = append(dirs, filepath.ToSlash(fields[len(fields)-1]))
		}
	}
	return dirs, nil
}

// loggedGoListOutput runs go list and returns its stdout. The -e flag keeps the
// package stream going when a generated embed target is absent, so detection can
// stay cheap and still see the package's .go files. It spawns a process, so it
// emits a boundary log.
func loggedGoListOutput() (string, error) {
	slog.Info("ci-changed go list deps")
	out, err := exec.Command("go", "list", "-e", "-deps", "-json", "./...").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// goListSourceFiles returns the repo-root-relative files of every non-standard
// package in the build and test graph, decoded from go list -e -deps -json. Files
// outside the repo (module cache, GOROOT) are dropped because a push cannot
// change them. It spawns a process, so it emits a boundary log.
func goListSourceFiles(root string) ([]string, error) {
	out, err := loggedGoListOutput()
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(out))
	files := make([]string, 0)
	for decoder.More() {
		var pkg goListPackage
		if decodeErr := decoder.Decode(&pkg); decodeErr != nil {
			return nil, decodeErr
		}
		if pkg.Standard || pkg.Dir == "" {
			continue
		}
		for _, group := range packageFileGroups(pkg) {
			for _, name := range group {
				rel, ok := repoRelative(root, filepath.Join(pkg.Dir, name))
				if ok {
					files = append(files, rel)
				}
			}
		}
	}
	return sortedUnique(files), nil
}

// packageFileGroups gathers every file-name slice that names a build or test
// input of one package.
func packageFileGroups(pkg goListPackage) [][]string {
	return [][]string{
		pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles,
		pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles,
		pkg.SysoFiles, pkg.EmbedFiles, pkg.TestGoFiles, pkg.XTestGoFiles,
		pkg.TestEmbedFiles, pkg.XTestEmbedFiles,
	}
}

// repoRelative converts an absolute path to a repo-root-relative slash path,
// reporting false when the path is outside the repo.
func repoRelative(root, full string) (string, bool) {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// splitNonEmptyLines splits text on newlines and drops blank lines.
func splitNonEmptyLines(text string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
