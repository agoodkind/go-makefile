package lint_test

import (
	"testing"

	"goodkind.io/go-makefile/internal/lint"
)

func TestStaticcheckDefaultExcludePaths(t *testing.T) {
	cases := []struct {
		name     string
		defaults string
		flags    string
		want     string
	}{
		{name: "production flags keep the test file entry", defaults: `_test\.go:`, flags: "-nolint_ban -no_tilde_path_literal", want: `_test\.go:`},
		{name: "a test flag removes the test file entry", defaults: `gen/,_test\.go:,vendor/`, flags: "-nolint_ban -testpackage", want: "gen/,vendor/"},
		{name: "a disabled test flag keeps the test file entry", defaults: `_test\.go:`, flags: "-testdouble=false -testsourcefile=0", want: `_test\.go:`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := lint.StaticcheckDefaultExcludePaths(testCase.defaults, testCase.flags)
			if got != testCase.want {
				t.Fatalf("StaticcheckDefaultExcludePaths = %q, want %q", got, testCase.want)
			}
		})
	}
}
