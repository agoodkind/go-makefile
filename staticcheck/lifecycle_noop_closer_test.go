package staticcheck

import (
	"testing"
)

func TestLifecycleNoopCloserFlagsEmptyBody(t *testing.T) {
	t.Parallel()

	source := `package ui

type noopCloser struct{}

func (noopCloser) Close(reason string) error { return nil }
`
	a := newLifecycleNoopCloserAnalyzer()
	diags := runAnalyzerOnSource(t, a, "app.go", source)
	wantOnce(t, diags, "[LIFECYCLE001]", "noopCloser")
}

func TestLifecycleNoopCloserAcceptsNamedCancel(t *testing.T) {
	t.Parallel()

	source := `package ui

type ctxCloser struct {
	cancel func()
}

func (c *ctxCloser) Close(reason string) error {
	c.cancel()
	return nil
}
`
	a := newLifecycleNoopCloserAnalyzer()
	diags := runAnalyzerOnSource(t, a, "app.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics when cancel is called, got %d: %v", len(diags), diags)
	}
}

func TestLifecycleNoopCloserSkipsAllowlist(t *testing.T) {
	t.Parallel()

	source := `package mitm

type mitmHTTPCloser struct{}

func (mitmHTTPCloser) Close(reason string) error { return nil }
`
	a := newLifecycleNoopCloserAnalyzer()
	if err := a.Flags.Set("allowlist", "example.com/sample.mitmHTTPCloser"); err != nil {
		t.Fatalf("set allowlist: %v", err)
	}
	diags := runAnalyzerOnSource(t, a, "mitm.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for allowlisted type, got %d: %v", len(diags), diags)
	}
}
