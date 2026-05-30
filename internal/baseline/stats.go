package baseline

// Stats is one component's baseline update result, derived from the baseline key
// set before and after the rewrite. The counts describe what changed in the
// file, independent of which mode produced the change, so rendered output never
// needs to name a mode.
type Stats struct {
	Label            string
	BaselinePath     string
	ScopePattern     string
	FindingsCaptured int
	Added            int
	Refreshed        int
	Removed          int
	Covered          int
	Remaining        int
}

// IsNoop reports whether the recorded key set was unchanged.
func (statistics Stats) IsNoop() bool {
	return statistics.Added == 0 && statistics.Removed == 0
}

// countMissing returns how many keys of a are absent from b.
func countMissing(a, b map[string]struct{}) int {
	missing := 0
	for key := range a {
		if _, present := b[key]; !present {
			missing++
		}
	}
	return missing
}

// countShared returns how many keys of a are also in b.
func countShared(a, b map[string]struct{}) int {
	shared := 0
	for key := range a {
		if _, present := b[key]; present {
			shared++
		}
	}
	return shared
}

// computeStats derives the neutral counts from the pre-write and post-write key
// sets plus the raw current findings. added/removed/refreshed compare the old
// and new baseline key sets; covered counts raw findings now represented in the
// new baseline; remaining is the new baseline key count.
func computeStats(
	label, baselinePath, scopePattern string,
	findingsLines []string,
	oldKeys, newKeys map[string]struct{},
) Stats {
	covered := 0
	for _, key := range findingKeyList(findingsLines) {
		if _, present := newKeys[key]; present {
			covered++
		}
	}
	return Stats{
		Label:            label,
		BaselinePath:     baselinePath,
		ScopePattern:     scopePattern,
		FindingsCaptured: len(findingsLines),
		Added:            countMissing(newKeys, oldKeys),
		Refreshed:        countShared(oldKeys, newKeys),
		Removed:          countMissing(oldKeys, newKeys),
		Covered:          covered,
		Remaining:        len(newKeys),
	}
}
