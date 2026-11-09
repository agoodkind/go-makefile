package notice_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/baseline"
	"goodkind.io/go-makefile/internal/notice"
)

func TestSourceIdentityPreservesGoCodeAndComments(t *testing.T) {
	const original = `package sample

//Collect adds the arguments.
//go:noinline
func Collect(first, second int) []any {
	type Local = int
	values := []any{first + second, 7, "value"}
	return append(values, []any{"end"}...)
}

func untouched() int { return 17 }
`

	for _, scenario := range []struct {
		name, before, after string
		equal               bool
	}{
		{"parameter layout", "first, second int", "first int,\nsecond int,", true},
		{"comment formatting", "//Collect", "// Collect", true},
		{"unrelated declaration", "return 17", "return 19", true},
		{"parameter name", "first", "left", false},
		{"literal", ", 7,", ", 9,", false},
		{"operator", "first + second", "first - second", false},
		{"absent ellipsis", `{"end"}...`, `{"end"}`, false},
		{"absent alias token", "type Local = int", "type Local int", false},
		{"comment content", "adds the arguments", "subtracts the arguments", false},
		{"directive", "go:noinline", "go:nosplit", false},
	} {
		t.Run(
			scenario.name,
			func(t *testing.T) {
				for _, anchor := range []string{"func Collect(", "//"} {
					old := sourceIdentity(t, original, anchor)
					current := sourceIdentity(
						t,
						strings.ReplaceAll(original, scenario.before, scenario.after),
						anchor,
					)
					if (old == current) != scenario.equal {
						t.Fatalf(
							"source identity equality at %q = %v, want %v",
							anchor,
							old == current,
							scenario.equal,
						)
					}
				}
			},
		)
	}
}

func TestDuplicateSourceKeyPreservesNormalBaselineEquivalence(t *testing.T) {
	const old = "sample.go:3:1: 3-10 lines are duplicate of `peer.go:20-27` (dupl)"
	const current = "sample.go:7:1: 7-18 lines are duplicate of `peer.go:30-41` (dupl)"
	if baseline.Key(old) == baseline.Key(current) {
		t.Fatal("ordinary baseline keys ignored embedded duplicate ranges")
	}

	if notice.SourceKey(old) != notice.SourceKey(current) {
		t.Fatal("historical source keys changed with duplicate range coordinates")
	}

	if notice.SourceKey(old) == notice.SourceKey(
		strings.ReplaceAll(current, "peer.go", "other.go"),
	) {
		t.Fatal("historical source key omitted the peer path")
	}
}

func sourceIdentity(t *testing.T, contents, anchor string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	for index, line := range strings.Split(contents, "\n") {
		if !strings.HasPrefix(line, anchor) {
			continue
		}

		finding := fmt.Sprintf("sample.go:%d:1: source finding (forbidigo)", index+1)
		identity, err := notice.SourceIdentity(make(map[string]notice.Source), finding, root)
		if err != nil {
			t.Fatal(err)
		}

		return identity
	}

	t.Fatalf("source fixture does not contain diagnostic anchor %q", anchor)

	return ""
}
