package staticcheck

import (
	"testing"
)

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
	wantOnce(t, diags, "[RTA002]", "self-confessed", "Frob")
}

func TestRTASyntheticMarkerCallIgnoresUnconfessedDiscard(t *testing.T) {
	t.Parallel()

	source := `package supervisor

type Meta struct {
	Kind string
}

func (m Meta) IsLivetrackMeta() bool { return m.Kind != "" }

var _ = Meta{}.IsLivetrackMeta()
`
	a := newRTASyntheticMarkerCallAnalyzer()
	diags := runAnalyzerOnSource(t, a, "registry.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics without confessional comment, got %d: %v", len(diags), diags)
	}
}

func TestRTASyntheticMarkerCallIgnoresVoidCall(t *testing.T) {
	t.Parallel()

	source := `package supervisor

type logger struct{}

func (logger) Debug(msg string) {}

func startup(log logger) {
	// reachability hack: keep Foo alive for the deadcode analyzer
	log.Debug("startup")
}
`
	a := newRTASyntheticMarkerCallAnalyzer()
	diags := runAnalyzerOnSource(t, a, "startup.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics on void call, got %d: %v", len(diags), diags)
	}
}
