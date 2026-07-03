package main

import (
	"os"
	"testing"
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
