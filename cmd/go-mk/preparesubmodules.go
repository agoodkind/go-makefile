// prepare-generated-submodules for go-mk. The generated-output cache restores a
// consumer's GO_MK_GENERATE_OUTPUTS onto disk, and those paths can live inside
// nested submodules. If the cache lands files there before the submodule is
// initialized, the consumer's codegen `git submodule update` fails on a
// non-empty directory. This command initializes exactly the submodules an output
// path descends through, before the cache restore. It derives them from the
// declared outputs and git's own .gitmodules, so it names no consumer library
// and never touches a submodule that holds no output.
package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// submoduleInit names one submodule to initialize. Parent is the repo-relative
// directory git runs in ("" is the repo root). Path is the submodule path
// relative to Parent.
type submoduleInit struct {
	Parent string
	Path   string
}

// submoduleInitPlan returns the ordered, de-duplicated submodule initializations
// needed so every output path's containing submodules exist. It walks the
// submodule nesting for each output: at each level it finds the declared
// submodule whose path prefixes the remaining output suffix, records it, and
// descends. listSubmodules(dir) returns the submodule paths in <dir>/.gitmodules
// relative to dir. Parents are always emitted before their children.
func submoduleInitPlan(outputs []string, listSubmodules func(dir string) ([]string, error)) ([]submoduleInit, error) {
	plan := make([]submoduleInit, 0, len(outputs))
	seen := map[string]bool{}
	for _, output := range outputs {
		remaining := path.Clean(strings.TrimSpace(output))
		if remaining == "" || remaining == "." {
			continue
		}
		parent := ""
		for {
			subs, err := listSubmodules(parent)
			if err != nil {
				return nil, err
			}
			match := longestSubmodulePrefix(subs, remaining)
			if match == "" {
				break
			}
			full := path.Join(parent, match)
			if !seen[full] {
				seen[full] = true
				plan = append(plan, submoduleInit{Parent: parent, Path: match})
			}
			parent = full
			remaining = strings.TrimPrefix(remaining[len(match):], "/")
		}
	}
	return plan, nil
}

// longestSubmodulePrefix returns the submodule path (from subs) that is a
// path-component prefix of target, choosing the longest when several match. It
// returns "" when none is a prefix.
func longestSubmodulePrefix(subs []string, target string) string {
	best := ""
	for _, sub := range subs {
		clean := path.Clean(sub)
		if clean == target || strings.HasPrefix(target, clean+"/") {
			if len(clean) > len(best) {
				best = clean
			}
		}
	}
	return best
}

// gitListSubmodules returns the submodule paths declared in <dir>/.gitmodules,
// each relative to dir. A missing .gitmodules yields an empty slice, since a
// level with no nested submodules is normal.
func gitListSubmodules(dir string) ([]string, error) {
	file, err := os.Open(filepath.Join(dir, ".gitmodules"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		slog.Error("prepare-generated-submodules open .gitmodules failed", slog.String("dir", dir), slog.Any("err", err))
		return nil, fmt.Errorf("prepare-generated-submodules: open .gitmodules in %q: %w", dir, err)
	}
	defer func() { _ = file.Close() }()
	var paths []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		value, ok := strings.CutPrefix(line, "path")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value, ok = strings.CutPrefix(value, "=")
		if !ok {
			continue
		}
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Error("prepare-generated-submodules read .gitmodules failed", slog.String("dir", dir), slog.Any("err", err))
		return nil, fmt.Errorf("prepare-generated-submodules: read .gitmodules in %q: %w", dir, err)
	}
	return paths, nil
}

// runPrepareGeneratedSubmodules initializes the submodules that contain the
// consumer's declared GO_MK_GENERATE_OUTPUTS, so a later generated-cache restore
// lands its files in initialized working trees. It is a no-op when no outputs
// are declared or none descend into a submodule.
func runPrepareGeneratedSubmodules() error {
	outputs := strings.Fields(os.Getenv("GO_MK_GENERATE_OUTPUTS"))
	if len(outputs) == 0 {
		return nil
	}
	plan, err := submoduleInitPlan(outputs, gitListSubmodules)
	if err != nil {
		slog.Error("prepare-generated-submodules plan failed", slog.Any("err", err))
		return err
	}
	slog.Info("prepare-generated-submodules", slog.Int("submodules", len(plan)))
	for _, entry := range plan {
		args := []string{}
		if entry.Parent != "" {
			args = append(args, "-C", entry.Parent)
		}
		args = append(args, "submodule", "update", "--init", "--", entry.Path)
		if err := runProcess("git", args, os.Environ()); err != nil {
			slog.Error("prepare-generated-submodules init failed",
				slog.String("parent", entry.Parent),
				slog.String("path", entry.Path),
				slog.Any("err", err))
			return fmt.Errorf("prepare-generated-submodules: init %q under %q: %w", entry.Path, entry.Parent, err)
		}
	}
	return nil
}
