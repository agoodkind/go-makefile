package main

import (
	"os"
	"testing"

	"goodkind.io/gklog/correlation"
)

func TestCacheManifestIsHeaderless(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	os.Args = []string{"go-mk", "cache-manifest"}
	if !headerless() {
		t.Fatal("cache-manifest should not print a run header")
	}

	os.Args = []string{"go-mk", "lint"}
	if headerless() {
		t.Fatal("lint should print a run header")
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
