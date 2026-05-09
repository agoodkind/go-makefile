package staticcheck

import (
	"testing"
)

func TestRTASyntheticMarkerCallFlagsCompositeBypass(t *testing.T) {
	t.Parallel()

	source := `package supervisor

type Meta struct {
	Kind          string
	LiveSessionID string
	PID           int
}

func (Meta) IsLivetrackMeta() bool { return true }

var _ = Meta{}.IsLivetrackMeta()
`
	a := newRTASyntheticMarkerCallAnalyzer()
	if err := a.Flags.Set("marker_methods", "IsLivetrackMeta"); err != nil {
		t.Fatalf("set marker_methods: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "registry.go", source)
	wantOnce(t, diags, "[RTA002]", "IsLivetrackMeta")
}

func TestRTASyntheticMarkerCallFlagsConfessionalComment(t *testing.T) {
	t.Parallel()

	source := `package supervisor

type any2 struct{}

func (any2) Frob() bool { return true }

func init() {
	// keep Frob reachable for the deadcode analyzer
	_ = any2{}.Frob()
}
`
	a := newRTASyntheticMarkerCallAnalyzer()
	diags := runAnalyzerOnSource(t, a, "registry.go", source)
	wantOnce(t, diags, "[RTA002]", "self-confessed")
}

func TestRTASyntheticMarkerCallAcceptsLegitimateUse(t *testing.T) {
	t.Parallel()

	source := `package supervisor

type Meta struct {
	Kind string
}

func (m Meta) IsLivetrackMeta() bool { return m.Kind != "" }

func DescribeMeta(m Meta) string {
	if !m.IsLivetrackMeta() {
		return "unset"
	}
	return m.Kind
}
`
	a := newRTASyntheticMarkerCallAnalyzer()
	if err := a.Flags.Set("marker_methods", "IsLivetrackMeta"); err != nil {
		t.Fatalf("set marker_methods: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "registry.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for legitimate use, got %d: %v", len(diags), diags)
	}
}
