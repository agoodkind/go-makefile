package staticcheck

import (
	"testing"
)

// TestRTASlogFieldBypassFlagsCLYDE308Shape covers the empirical
// CLYDE-308 phase 2 case: a marker-typed value is constructed
// locally and passed to slog.InfoContext as a key-value attribute
// with no other consumer. The marker method IsLivetrackMeta is
// declared with an empty body alongside the boxing site.
func TestRTASlogFieldBypassFlagsCLYDE308Shape(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type SupervisorMeta struct {
	Kind string
}

func (SupervisorMeta) IsLivetrackMeta() {}

func startup(ctx context.Context) {
	supervisorMeta := SupervisorMeta{Kind: "supervisor"}
	slog.InfoContext(ctx, "supervisor.starting", "supervisor_meta", supervisorMeta)
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	wantOnce(t, diags, "[RTA004]", "supervisor_meta", "IsLivetrackMeta")
}

// TestRTASlogFieldBypassFlagsInlineCompositeLit covers the inline
// construction shape: the marker value is built directly inside
// the slog call's argument list with no intermediate variable. The
// detector should still fire because the value has no consumer
// outside the slog call.
func TestRTASlogFieldBypassFlagsInlineCompositeLit(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type SupervisorMeta struct {
	Kind string
}

func (SupervisorMeta) IsLivetrackMeta() {}

func startup(ctx context.Context) {
	slog.InfoContext(ctx, "supervisor.starting", "supervisor_meta", SupervisorMeta{Kind: "supervisor"})
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	wantOnce(t, diags, "[RTA004]", "supervisor_meta")
}

// TestRTASlogFieldBypassAcceptsRealConsumer covers the false-
// positive guard: the same marker-typed value also flows into a
// non-slog consumer (a registry call). This indicates the value is
// load-bearing in the function and the slog field is incidental, so
// the detector must skip it.
func TestRTASlogFieldBypassAcceptsRealConsumer(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type SupervisorMeta struct {
	Kind string
}

func (SupervisorMeta) IsLivetrackMeta() {}

func register(meta SupervisorMeta) {}

func startup(ctx context.Context) {
	supervisorMeta := SupervisorMeta{Kind: "supervisor"}
	register(supervisorMeta)
	slog.InfoContext(ctx, "supervisor.starting", "supervisor_meta", supervisorMeta)
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics when value has a real consumer, got %d: %v", len(diags), diags)
	}
}

// TestRTASlogFieldBypassAcceptsNonEmptyMarkerBody covers the
// empty-body requirement. A marker-named method with a non-empty
// body is treated as doing real work, so the detector must skip.
func TestRTASlogFieldBypassAcceptsNonEmptyMarkerBody(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type SupervisorMeta struct {
	Kind string
}

func (m SupervisorMeta) IsLivetrackMeta() {
	_ = m.Kind
}

func startup(ctx context.Context) {
	supervisorMeta := SupervisorMeta{Kind: "supervisor"}
	slog.InfoContext(ctx, "supervisor.starting", "supervisor_meta", supervisorMeta)
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics when marker body is non-empty, got %d: %v", len(diags), diags)
	}
}

// TestRTASlogFieldBypassAcceptsLegitimateLogging covers a normal
// structured log call where the value is a plain string and no
// marker type is involved. The detector must not fire.
func TestRTASlogFieldBypassAcceptsLegitimateLogging(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

func startup(ctx context.Context) {
	slog.InfoContext(ctx, "supervisor.starting", "kind", "supervisor")
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics on legitimate slog call, got %d: %v", len(diags), diags)
	}
}

// TestRTASlogFieldBypassAcceptsNonMarkerStruct covers a structured
// log call whose value is a struct that does not carry any method
// matching the marker set. The detector must not fire.
func TestRTASlogFieldBypassAcceptsNonMarkerStruct(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type Detail struct {
	Kind string
}

func startup(ctx context.Context) {
	d := Detail{Kind: "supervisor"}
	slog.InfoContext(ctx, "supervisor.starting", "detail", d)
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics on non-marker struct, got %d: %v", len(diags), diags)
	}
}

// TestRTASlogFieldBypassFlagsLoggerMethodReceiver covers the
// receiver-method shape: instead of slog.InfoContext, the call is
// log.InfoContext on a *slog.Logger value. The detector must still
// fire because isLikelyLoggerReceiver matches the "log" receiver.
func TestRTASlogFieldBypassFlagsLoggerMethodReceiver(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type SupervisorMeta struct {
	Kind string
}

func (SupervisorMeta) IsLivetrackMeta() {}

func startup(ctx context.Context, log *slog.Logger) {
	supervisorMeta := SupervisorMeta{Kind: "supervisor"}
	log.InfoContext(ctx, "supervisor.starting", "supervisor_meta", supervisorMeta)
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	wantOnce(t, diags, "[RTA004]", "supervisor_meta")
}

// TestRTASlogFieldBypassRespectsMarkerMethodsFlag covers the
// configurable marker name set: a custom method name set via the
// -marker_methods flag is honoured, and the default name no longer
// fires. The fixture defines a marker named ItIsAMarker and the
// flag overrides the default to match it.
func TestRTASlogFieldBypassRespectsMarkerMethodsFlag(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type SupervisorMeta struct {
	Kind string
}

func (SupervisorMeta) ItIsAMarker() {}

func startup(ctx context.Context) {
	supervisorMeta := SupervisorMeta{Kind: "supervisor"}
	slog.InfoContext(ctx, "supervisor.starting", "supervisor_meta", supervisorMeta)
}
`
	a := newRTASlogFieldBypassAnalyzer()
	if err := a.Flags.Set("marker_methods", "ItIsAMarker"); err != nil {
		t.Fatalf("a.Flags.Set returned error: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	wantOnce(t, diags, "[RTA004]", "ItIsAMarker")
}

// TestRTASlogFieldBypassAcceptsMarkerMethodWithReturn covers the
// signature requirement: a marker-named method that takes args or
// returns a value does not match the no-arg no-return signature, so
// the type is not classified as a marker and the detector skips.
func TestRTASlogFieldBypassAcceptsMarkerMethodWithReturn(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type Meta struct {
	Kind string
}

func (Meta) IsLivetrackMeta() bool { return true }

func startup(ctx context.Context) {
	m := Meta{Kind: "supervisor"}
	slog.InfoContext(ctx, "starting", "meta", m)
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics when marker method has a return, got %d: %v", len(diags), diags)
	}
}

// TestRTASlogFieldBypassAcceptsValueAssignedToField covers a
// real-consumer shape where the value flows into a struct field
// before the slog call. Storing the value to an externally
// observable location is treated as a real consumer.
func TestRTASlogFieldBypassAcceptsValueAssignedToField(t *testing.T) {
	t.Parallel()

	source := `package supervisor

import (
	"context"
	"log/slog"
)

type SupervisorMeta struct {
	Kind string
}

func (SupervisorMeta) IsLivetrackMeta() {}

type holder struct {
	meta SupervisorMeta
}

func (h *holder) startup(ctx context.Context) {
	supervisorMeta := SupervisorMeta{Kind: "supervisor"}
	h.meta = supervisorMeta
	slog.InfoContext(ctx, "supervisor.starting", "supervisor_meta", supervisorMeta)
}
`
	a := newRTASlogFieldBypassAnalyzer()
	diags := runAnalyzerOnSource(t, a, "supervisor.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics when value flows to a struct field, got %d: %v", len(diags), diags)
	}
}
