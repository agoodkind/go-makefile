package report

import "testing"

func TestMarkerRoundTrip(t *testing.T) {
	original := GateMarker{
		Name:        "lint-gocyclo",
		Passed:      false,
		Findings:    []string{"internal/foo/bar.go:42:1: gocyclo: complexity 21 over 20 in bigFunc"},
		Remediation: "run the baseline refresh and re-run the gate.",
	}
	line, err := EncodeMarker(original)
	if err != nil {
		t.Fatalf("EncodeMarker: %v", err)
	}
	if line[:len(MarkerPrefix)] != MarkerPrefix {
		t.Fatalf("encoded line missing prefix: %q", line)
	}
	decoded, ok := ParseMarker(line)
	if !ok {
		t.Fatalf("ParseMarker returned not-ok for %q", line)
	}
	if decoded.Name != original.Name || decoded.Passed != original.Passed {
		t.Errorf("name/passed mismatch: %+v", decoded)
	}
	if len(decoded.Findings) != 1 || decoded.Findings[0] != original.Findings[0] {
		t.Errorf("findings mismatch: %+v", decoded.Findings)
	}
}

func TestParseMarkerRejectsPlainLine(t *testing.T) {
	if _, ok := ParseMarker("golangci-lint: OK"); ok {
		t.Error("plain line should not parse as a marker")
	}
}
