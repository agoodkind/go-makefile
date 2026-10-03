package baseline_test

import (
	"reflect"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/baseline"
)

const (
	plainOldBaseline = `# sample: generated_at=2026-01-01T00:00:00Z
old.go:10:2: fixed finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
keep.go:20:2: existing finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z`

	plainCurrentFindings = `keep.go:21:2: existing finding
new.go:30:2: new finding`

	scopedOldBaseline = `# sample: generated_at=2026-01-01T00:00:00Z
old-scoped.go:10:2: fixed scoped_rule finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
old-other.go:11:2: unrelated saved finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z
keep-scoped.go:20:2: existing scoped_rule finding	# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z`

	scopedCurrentFindings = `keep-scoped.go:21:2: existing scoped_rule finding
new-scoped.go:30:2: new scoped_rule finding`

	scopePattern = "scoped_rule"
)

func TestRewriteBody(t *testing.T) {
	cases := []struct {
		name    string
		old     string
		current string
		mode    string
		scope   string
		want    []string
	}{
		{"plain/sync", plainOldBaseline, plainCurrentFindings, "sync", "", []string{
			"keep.go:21:2: existing finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=NOW",
			"new.go:30:2: new finding\t# sample:first_added=NOW last_seen=NOW",
		}},
		{"plain/prune-fixed", plainOldBaseline, plainCurrentFindings, "prune-fixed", "", []string{
			"keep.go:21:2: existing finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=NOW",
		}},
		{"plain/remove-fixed", plainOldBaseline, plainCurrentFindings, "remove-fixed", "", []string{
			"keep.go:21:2: existing finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=NOW",
		}},
		{"plain/accept-new", plainOldBaseline, plainCurrentFindings, "accept-new", "", []string{
			"keep.go:21:2: existing finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=NOW",
			"new.go:30:2: new finding\t# sample:first_added=NOW last_seen=NOW",
			"old.go:10:2: fixed finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z",
		}},
		{"scoped/sync", scopedOldBaseline, scopedCurrentFindings, "sync", scopePattern, []string{
			"keep-scoped.go:21:2: existing scoped_rule finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=NOW",
			"new-scoped.go:30:2: new scoped_rule finding\t# sample:first_added=NOW last_seen=NOW",
			"old-other.go:11:2: unrelated saved finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z",
		}},
		{"scoped/prune-fixed", scopedOldBaseline, scopedCurrentFindings, "prune-fixed", scopePattern, []string{
			"keep-scoped.go:21:2: existing scoped_rule finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=NOW",
			"old-other.go:11:2: unrelated saved finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z",
		}},
		{"scoped/accept-new", scopedOldBaseline, scopedCurrentFindings, "accept-new", scopePattern, []string{
			"keep-scoped.go:21:2: existing scoped_rule finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=NOW",
			"new-scoped.go:30:2: new scoped_rule finding\t# sample:first_added=NOW last_seen=NOW",
			"old-scoped.go:10:2: fixed scoped_rule finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z",
			"old-other.go:11:2: unrelated saved finding\t# sample:first_added=2026-01-01T00:00:00Z last_seen=2026-01-01T00:00:00Z",
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mode, err := baseline.ParseMode(testCase.mode)
			if err != nil {
				t.Fatalf("ParseMode(%q): %v", testCase.mode, err)
			}
			got, err := baseline.RewriteBody(baseline.RewriteInput{
				CurrentLines: strings.Split(testCase.current, "\n"),
				OldLines:     strings.Split(testCase.old, "\n"),
				Label:        "sample",
				Now:          "NOW",
				ScopePattern: testCase.scope,
				Mode:         mode,
			})
			if err != nil {
				t.Fatalf("RewriteBody: %v", err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("RewriteBody %s\ngot:  %q\nwant: %q", testCase.name, got, testCase.want)
			}
		})
	}
}
