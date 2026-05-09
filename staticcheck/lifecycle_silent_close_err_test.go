package staticcheck

import (
	"testing"
)

func TestLifecycleSilentCloseErrFlagsBypass(t *testing.T) {
	t.Parallel()

	source := `package ws

type session struct {
	Conn closer
}

type closer interface {
	Close() error
}

type wsConnCloser struct {
	session *session
}

func (c *wsConnCloser) Close(reason string) error {
	_ = c.session.Conn.Close()
	return nil
}
`
	a := newLifecycleSilentCloseErrAnalyzer()
	diags := runAnalyzerOnSource(t, a, "ws.go", source)
	wantOnce(t, diags, "[LIFECYCLE002]", "Close")
}

func TestLifecycleSilentCloseErrAcceptsWrappedReturn(t *testing.T) {
	t.Parallel()

	source := `package ws

import "fmt"

type session struct {
	Conn closer
}

type closer interface {
	Close() error
}

type wsConnCloser struct {
	session *session
}

func (c *wsConnCloser) Close(reason string) error {
	if err := c.session.Conn.Close(); err != nil {
		return fmt.Errorf("close ws: %w", err)
	}
	return nil
}
`
	a := newLifecycleSilentCloseErrAnalyzer()
	diags := runAnalyzerOnSource(t, a, "ws.go", source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics when error is wrapped, got %d: %v", len(diags), diags)
	}
}
