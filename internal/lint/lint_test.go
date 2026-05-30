package lint

import (
	"reflect"
	"testing"
)

func TestExcludePattern(t *testing.T) {
	cases := []struct {
		name  string
		defs  string
		extra string
		want  string
	}{
		{name: "default only", defs: `_test\.go:`, extra: "", want: `_test\.go:`},
		{name: "both", defs: `_test\.go:`, extra: `gen/:vendor/`, want: `_test\.go:|gen/:vendor/`},
		{name: "empty", defs: "", extra: "", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ExcludePattern(testCase.defs, testCase.extra)
			if got != testCase.want {
				t.Fatalf("ExcludePattern = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestGolangciScopePattern(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		rule     string
		linter   string
		want     string
	}{
		{name: "explicit wins", explicit: "custom", rule: "r", linter: "l", want: "custom"},
		{name: "rule and linter", rule: "file-length-limit", linter: "revive", want: `file-length-limit:.*\(revive\)$`},
		{name: "rule only", rule: "file-length-limit", want: "file-length-limit:"},
		{name: "linter only", linter: "revive", want: `\(revive\)$`},
		{name: "none", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := GolangciScopePattern(testCase.explicit, testCase.rule, testCase.linter)
			if got != testCase.want {
				t.Fatalf("GolangciScopePattern = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestGocycloTransform(t *testing.T) {
	lines := []string{
		"42 main run cmd/go-mk/lint.go:12:1",
		"too short",
		"31 pkg (T) Method internal/lint/lint.go:99:1",
	}
	got := GocycloTransform(lines, 30)
	want := []string{
		"cmd/go-mk/lint.go:12:1: gocyclo: complexity 42 over 30 in main run",
		"internal/lint/lint.go:99:1: gocyclo: complexity 31 over 30 in pkg (T) Method",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GocycloTransform = %#v, want %#v", got, want)
	}
}

func TestScopedPackagesFromFiles(t *testing.T) {
	got := ScopedPackagesFromFiles([]string{"a/b/c.go", "a/b/d.go", "x/y.go"})
	want := []string{"./a/b", "./x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScopedPackagesFromFiles = %#v, want %#v", got, want)
	}
}

func TestFilterScopedFindings(t *testing.T) {
	lines := []string{
		"a/b/c.go:12:1: finding",
		"a/b/other.go:1:1: finding",
		"x/y.go:3:1: finding",
	}
	got := FilterScopedFindings(lines, []string{"a/b/c.go", "x/y.go"})
	want := []string{"a/b/c.go:12:1: finding", "x/y.go:3:1: finding"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterScopedFindings = %#v, want %#v", got, want)
	}
}

func TestFilterMakeErrorLines(t *testing.T) {
	lines := []string{
		"golangci-lint: OK",
		"make: *** [lint-golangci] Error 1",
		"make[1]: *** [lint-format] Error 2",
		"keep this",
	}
	got := FilterMakeErrorLines(lines)
	want := []string{"golangci-lint: OK", "keep this"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterMakeErrorLines = %#v, want %#v", got, want)
	}
}

func TestDedupeFailedGates(t *testing.T) {
	got := DedupeFailedGates([]string{"lint-format", "lint-format", "", "lint-gocyclo"})
	want := []string{"lint-format", "lint-gocyclo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DedupeFailedGates = %#v, want %#v", got, want)
	}
}

func TestSlugify(t *testing.T) {
	if got := Slugify("Hello, World! 123"); got != "helloworld123" {
		t.Fatalf("Slugify = %q", got)
	}
	if got := Slugify("Foo-Bar_baz"); got != "foo-bar_baz" {
		t.Fatalf("Slugify = %q", got)
	}
}
