package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGateRunsCheckLocally(t *testing.T) {
	called := false
	status := runBuildGateWith(buildGateConfig{
		proofValid: func() bool {
			return false
		},
		buildCheck: func() int {
			called = true
			return 17
		},
		stdout: func(_ string) {},
	})
	if !called {
		t.Fatal("build gate did not run build-check without CI proof")
	}
	if status != 17 {
		t.Fatalf("status = %d, want 17", status)
	}
}

func TestBuildGateSkipsCheckWithValidProof(t *testing.T) {
	called := false
	var output strings.Builder
	status := runBuildGateWith(buildGateConfig{
		proofValid: func() bool {
			return true
		},
		buildCheck: func() int {
			called = true
			return 17
		},
		stdout: func(text string) {
			output.WriteString(text)
		},
	})
	if called {
		t.Fatal("build gate ran build-check despite valid CI proof")
	}
	if status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if !strings.Contains(output.String(), "OIDC proof verified") {
		t.Fatalf("output = %q, want OIDC proof note", output.String())
	}
}

func TestBuildGateRunsCheckWhenProofInvalid(t *testing.T) {
	called := false
	status := runBuildGateWith(buildGateConfig{
		proofValid: func() bool {
			return false
		},
		buildCheck: func() int {
			called = true
			return 23
		},
		stdout: func(_ string) {},
	})
	if !called {
		t.Fatal("build gate did not run build-check when CI proof was invalid")
	}
	if status != 23 {
		t.Fatalf("status = %d, want 23", status)
	}
}

func TestLegacyBypassNamesRemovedFromBlockingGateSurfaces(t *testing.T) {
	legacyNames := []string{
		"BYPASS" + "_LINT",
		"BYPASS" + "_CONFIRM",
		"BYPASS" + "_TOKEN_CMD",
	}
	paths := []string{
		"buildcheck.go",
		"lint_chain.go",
		filepath.Join("..", "..", "go.mk"),
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, legacyName := range legacyNames {
			if strings.Contains(string(body), legacyName) {
				t.Fatalf("%s still references %s", path, legacyName)
			}
		}
	}
}
