package staticcheck

import (
	"testing"
)

func TestRTASlogMarkerBoxFlagsMarkerStruct(t *testing.T) {
	t.Parallel()

	source := `package webapp

import "log/slog"

type WebMeta struct {
	ChannelID string
}

func (WebMeta) IsLivetrackMeta() bool { return true }

func startWebApp(log *slog.Logger, id string) {
	log.Info("webapp.starting", "channel_meta", WebMeta{ChannelID: id})
}
`
	a := newRTASlogMarkerBoxAnalyzer()
	if err := a.Flags.Set("marker_methods", "IsLivetrackMeta"); err != nil {
		t.Fatalf("set marker_methods: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "webapp.go", source)
	wantOnce(t, diags, "[RTA001]", "WebMeta")
}

func TestRTASlogMarkerBoxAcceptsPrimitiveFields(t *testing.T) {
	t.Parallel()

	source := `package webapp

import "log/slog"

type WebMeta struct {
	ChannelID string
}

func (WebMeta) IsLivetrackMeta() bool { return true }

func startWebApp(log *slog.Logger, id string) {
	log.Info("webapp.starting", "channel_id", id)
}
`
	a := newRTASlogMarkerBoxAnalyzer()
	if err := a.Flags.Set("marker_methods", "IsLivetrackMeta"); err != nil {
		t.Fatalf("set marker_methods: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "webapp.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for primitive slog field, got %d: %v", len(diags), diags)
	}
}
