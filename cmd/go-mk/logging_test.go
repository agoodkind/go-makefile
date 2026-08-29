package main

import (
	"os"
	"testing"

	"goodkind.io/gklog/correlation"
)

func TestHeaderlessCommands(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	os.Args = []string{"go-mk", "cache-manifest"}
	if !headerless() {
		t.Fatal("cache-manifest should be headerless")
	}

	os.Args = []string{"go-mk", "lint"}
	if headerless() {
		t.Fatal("lint should be visible")
	}
}

func TestCapabilityProbe(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	os.Args = []string{"go-mk", "-flags"}
	if !capabilityProbe() {
		t.Fatal("-flags should bypass logging")
	}

	os.Args = []string{"go-mk", "lint"}
	if capabilityProbe() {
		t.Fatal("lint should not bypass logging")
	}
}

func TestRunHeaderHasNoLeadingSymbol(t *testing.T) {
	corr := correlation.Context{
		TraceID: correlation.TraceID("123"),
		SpanID:  correlation.SpanID("456"),
	}

	want := "logs=.make/logs trace_id=123 span_id=456"
	if got := runHeaderLine(corr); got != want {
		t.Fatalf("run header = %q, want %q", got, want)
	}
}
