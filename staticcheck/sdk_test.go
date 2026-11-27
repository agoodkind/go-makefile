package staticcheck

import (
	"errors"
	"go/build"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCommandEntrySDKResolution(t *testing.T) {
	t.Parallel()
	missingSDK := filepath.Join(t.TempDir(), "missing-sdk")
	lookupFailure := errors.New("go is unavailable")

	for _, scenario := range []struct {
		name         string
		embeddedRoot string
		queriedRoot  string
		lookupError  error
		wantLookup   bool
		wantError    bool
	}{
		{name: "installed embedded SDK", embeddedRoot: runtime.GOROOT()},
		{name: "trimmed binary", queriedRoot: runtime.GOROOT() + "\n", wantLookup: true},
		{
			name:         "relocated SDK",
			embeddedRoot: missingSDK,
			queriedRoot:  runtime.GOROOT(),
			wantLookup:   true,
		},
		{name: "missing go command", lookupError: lookupFailure, wantLookup: true, wantError: true},
		{name: "empty GOROOT", wantLookup: true, wantError: true},
		{
			name:        "missing standard library",
			queriedRoot: missingSDK,
			wantLookup:  true,
			wantError:   true,
		},
	} {
		t.Run(
			scenario.name,
			func(t *testing.T) {
				t.Parallel()
				buildContext := build.Default
				buildContext.GOROOT = scenario.embeddedRoot
				lookedUp := false
				resolved, err := resolveCommandEntryBuildContext(
					buildContext,
					func() (string, error) {
						lookedUp = true

						return scenario.queriedRoot, scenario.lookupError
					},
				)
				if lookedUp != scenario.wantLookup {
					t.Fatalf("queried installed SDK = %t, want %t", lookedUp, scenario.wantLookup)
				}

				if scenario.wantError {
					if !errors.Is(err, errCommandEntrySDK) {
						t.Fatalf("SDK error = %v, want %v", err, errCommandEntrySDK)
					}

					if scenario.lookupError != nil && !errors.Is(err, scenario.lookupError) {
						t.Fatalf("SDK error lost the command failure: %v", err)
					}

					return
				}

				if err != nil {
					t.Fatal(err)
				}

				metadata, err := resolved.Import("fmt", "", build.FindOnly)
				if err != nil || !metadata.Goroot {
					t.Fatalf("resolved SDK cannot classify fmt as a standard package: %v", err)
				}
			},
		)
	}
}
