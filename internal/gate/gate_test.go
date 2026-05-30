package gate

import "testing"

func TestConfirmAccepted(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{value: "1", want: true},
		{value: "y", want: true},
		{value: "yes", want: true},
		{value: "Y", want: true},
		{value: "YES", want: true},
		{value: "", want: false},
		{value: "0", want: false},
		{value: "no", want: false},
		{value: "true", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.value, func(t *testing.T) {
			if got := ConfirmAccepted(testCase.value); got != testCase.want {
				t.Fatalf("ConfirmAccepted(%q) = %v, want %v", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestTokensMatch(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		actual   string
		want     bool
	}{
		{name: "exact", expected: "Hurricane Tina", actual: "Hurricane Tina", want: true},
		{name: "slug equal", expected: "Foo-Bar! Baz", actual: "foo-bar_baz", want: false},
		{name: "case folds", expected: "ABC", actual: "abc", want: true},
		{name: "empty expected", expected: "", actual: "abc", want: false},
		{name: "empty actual", expected: "abc", actual: "", want: false},
		{name: "both empty", expected: "", actual: "", want: false},
		{name: "mismatch", expected: "alpha", actual: "beta", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := TokensMatch(testCase.expected, testCase.actual); got != testCase.want {
				t.Fatalf("TokensMatch(%q, %q) = %v, want %v", testCase.expected, testCase.actual, got, testCase.want)
			}
		})
	}
}
