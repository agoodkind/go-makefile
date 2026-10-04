package baseline

import "testing"

func TestKeyStripsDotDotAndCollapsesLineCol(t *testing.T) {
	cases := map[string]string{
		"a.go:10:2: msg":           "a.go::: msg",
		"../../a.go:10:2: msg":     "a.go::: msg",
		"pkg/a.go:1:1: x (linter)": "pkg/a.go::: x (linter)",
		"no-coordinate line":       "no-coordinate line",
	}
	for input, want := range cases {
		if got := Key(input); got != want {
			t.Errorf("Key(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseModeAcceptsRemoveFixedAlias(t *testing.T) {
	for _, value := range []string{"prune-fixed", "remove-fixed"} {
		mode, err := ParseMode(value)
		if err != nil {
			t.Fatalf("ParseMode(%q) error: %v", value, err)
		}
		if mode != ModePruneFixed {
			t.Errorf("ParseMode(%q) = %v, want ModePruneFixed", value, mode)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Error("ParseMode(bogus) expected error")
	}
}
