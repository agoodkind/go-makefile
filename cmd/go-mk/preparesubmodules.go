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
	"path"
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
