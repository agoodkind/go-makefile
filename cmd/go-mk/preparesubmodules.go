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

// prepareSubmodulesForOutputs initializes, top-down, the submodules that each
// output path descends through. It interleaves discovery and init: a nested
// .gitmodules is only readable after its parent submodule is checked out, so the
// walk initializes each submodule before descending to read the next level. A
// submodule that holds no declared output is never matched, so it is never
// initialized. listSubmodules(dir) returns the submodule paths in
// <dir>/.gitmodules relative to dir; initSubmodule(parent, submodulePath)
// initializes <submodulePath> under <parent> ("" is the repo root).
func prepareSubmodulesForOutputs(
	outputs []string,
	listSubmodules func(dir string) ([]string, error),
	initSubmodule func(parent, submodulePath string) error,
) error {
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
				return err
			}
			match := longestSubmodulePrefix(subs, remaining)
			if match == "" {
				break
			}
			full := path.Join(parent, match)
			if !seen[full] {
				seen[full] = true
				if err := initSubmodule(parent, match); err != nil {
					return err
				}
			}
			parent = full
			remaining = strings.TrimPrefix(remaining[len(match):], "/")
		}
	}
	return nil
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
// level with no nested submodules (or a not-yet-initialized submodule) is
// normal.
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

// gitInitSubmodule initializes submodulePath under parent, running git in parent
// ("" is the repo root) so a nested submodule initializes inside its already
// checked-out parent. It inherits the workflow environment for git auth.
func gitInitSubmodule(parent, submodulePath string) error {
	args := []string{}
	if parent != "" {
		args = append(args, "-C", parent)
	}
	args = append(args, "submodule", "update", "--init", "--", submodulePath)
	if err := runProcess("git", args, nil); err != nil {
		wrapped := fmt.Errorf("prepare-generated-submodules: init %q under %q: %w", submodulePath, parent, err)
		slog.Error("prepare-generated-submodules init failed",
			slog.String("parent", parent),
			slog.String("path", submodulePath),
			slog.Any("err", wrapped))
		return wrapped
	}
	return nil
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
	slog.Info("prepare-generated-submodules", slog.Int("outputs", len(outputs)))
	return prepareSubmodulesForOutputs(outputs, gitListSubmodules, gitInitSubmodule)
}
