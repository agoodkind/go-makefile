// Nested git working tree discovery and target filtering for go-mk lint
// gates. The lint gates walk the source tree from the current working
// directory; when a Claude Code (or similar) git worktree of the same repo
// is placed inside the main checkout (a common convention for tools that
// stage work in `.claude/worktrees/<name>/`), the gates would otherwise
// recurse into the nested checkout and lint a parallel copy of the
// repository. This file detects such nested working trees structurally and
// provides helpers each gate calls before invoking its tool.
//
// Detection is name-agnostic: a directory is treated as a nested working
// tree when it contains a `.git` entry (file or directory) of its own. A
// `.git` file means a git worktree (`git worktree add`); a `.git` directory
// means an ordinary nested clone or a submodule. Either way the subtree
// belongs to a different working tree and lint should leave it alone.
//
// Discovery starts from the current working directory and walks downward
// only, so a lint run executed from inside a nested worktree finds no
// nested worktrees and lints the worktree's own code normally.
//
// Set GO_MK_LINT_INCLUDE_NESTED_WORKTREES=1 to disable the filter.
package main

import (
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// nestedWorktreeRoots walks from the current working directory and returns
// the set of subdirectory paths that are themselves git working trees
// nested inside the current working tree, expressed as "./<path>" with
// forward slashes. The current working tree's own `.git` marker is excluded
// from the result.
//
// When GO_MK_LINT_INCLUDE_NESTED_WORKTREES=1 is set the function returns an
// empty set, which makes the per-gate filter helpers a no-op.
func nestedWorktreeRoots() (map[string]struct{}, error) {
	if isTruthy(os.Getenv("GO_MK_LINT_INCLUDE_NESTED_WORKTREES")) {
		return map[string]struct{}{}, nil
	}
	roots := map[string]struct{}{}
	walkErr := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != ".git" {
			return nil
		}
		parent := filepath.Dir(path)
		if parent == "." {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		roots["./"+filepath.ToSlash(parent)] = struct{}{}
		return filepath.SkipDir
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(roots) > 0 {
		logNestedWorktreeRoots(roots)
	}
	return roots, nil
}

// logNestedWorktreeRoots emits a single structured log line listing the
// nested worktree roots that lint gates will skip. The roots are sorted so
// the log is stable across runs.
func logNestedWorktreeRoots(roots map[string]struct{}) {
	sorted := make([]string, 0, len(roots))
	for root := range roots {
		sorted = append(sorted, root)
	}
	slices.Sort(sorted)
	slog.Info("lint skip nested worktrees", slog.Any("roots", sorted))
}

// pathInAnyRoot reports whether p lies at or below any of the given root
// paths. Both p and roots are expected to use the "./<path>" form.
func pathInAnyRoot(p string, roots map[string]struct{}) bool {
	normalized := normalizePathForRootMatch(p)
	for root := range roots {
		if normalized == root || strings.HasPrefix(normalized, root+"/") {
			return true
		}
	}
	return false
}

// normalizePathForRootMatch returns p in "./<slashpath>" form so that
// pathInAnyRoot can compare it to roots produced by nestedWorktreeRoots.
func normalizePathForRootMatch(p string) string {
	p = filepath.ToSlash(p)
	if p == "." || strings.HasPrefix(p, "./") {
		return p
	}
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "./" + p
}

// dropPathsUnderNestedWorktrees returns paths with any entry that lies at
// or below a nested working tree root removed. An empty roots set is a
// pass-through.
func dropPathsUnderNestedWorktrees(paths []string, roots map[string]struct{}) []string {
	if len(roots) == 0 {
		return paths
	}
	kept := make([]string, 0, len(paths))
	for _, p := range paths {
		if pathInAnyRoot(p, roots) {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

// expandedPackageTargets resolves a list of package patterns to pass to a
// lint tool. When targets contains the literal "./..." and at least one
// nested working tree exists, the pattern is expanded via `go list ./...`
// and any package whose directory sits under a nested worktree root is
// dropped. Other entries pass through unchanged. The returned slice is
// stable-sorted.
//
// When there are no nested worktrees or no "./..." pattern, targets is
// returned unchanged so the tool sees exactly what the user configured.
func expandedPackageTargets(targets []string) ([]string, error) {
	roots, err := nestedWorktreeRoots()
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return targets, nil
	}
	if !slices.Contains(targets, "./...") {
		return targets, nil
	}
	pkgs, err := listFilteredPackages(roots)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(targets)+len(pkgs))
	expanded := false
	for _, t := range targets {
		if t == "./..." {
			if !expanded {
				result = append(result, pkgs...)
				expanded = true
			}
			continue
		}
		result = append(result, t)
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

// listFilteredPackages runs `go list -f '{{.Dir}}\t{{.ImportPath}}' ./...`
// and returns the import paths of packages whose directory is not under
// any of the given nested worktree roots. The directory path is compared
// after being made relative to the current working directory.
func listFilteredPackages(roots map[string]struct{}) ([]string, error) {
	slog.Info("lint list packages for nested worktree filter")
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}\t{{.ImportPath}}", "./...").Output()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	pkgs := make([]string, 0)
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		rel, relErr := filepath.Rel(cwd, parts[0])
		if relErr != nil {
			continue
		}
		if pathInAnyRoot("./"+filepath.ToSlash(rel), roots) {
			continue
		}
		pkgs = append(pkgs, parts[1])
	}
	slices.Sort(pkgs)
	return slices.Compact(pkgs), nil
}

// truthyEnvValues enumerates the env-var-style strings that count as a
// "yes, this toggle is on" answer. Stored as a set so callers can swap in
// new spellings without growing a switch statement (and so the
// string_switch_should_be_enum analyzer stays happy with this tiny lookup).
var truthyEnvValues = map[string]struct{}{
	"1":    {},
	"true": {},
	"yes":  {},
	"on":   {},
}

// isTruthy reports whether the env-var-style string represents an enabled
// boolean. It mirrors how Make-style toggles (1, true, yes, on) are
// commonly read in shell scripts.
func isTruthy(value string) bool {
	_, ok := truthyEnvValues[strings.ToLower(strings.TrimSpace(value))]
	return ok
}
