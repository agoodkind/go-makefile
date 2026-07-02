package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"goodkind.io/go-makefile/selfupdate"
)

func TestRunVerifyReleasePassesConfigAndPrintsCount(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	var capturedOptions selfupdate.Options
	var capturedTag string
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runVerifyRelease(
		context.Background(),
		[]string{
			"--repo",
			"agoodkind/agent-gate",
			"--tag",
			"v1.2.3",
			"--binary",
			"agent-gate",
			"--api-base",
			"https://api.example.invalid",
		},
		&stdout,
		&stderr,
		func(_ context.Context, options selfupdate.Options, tag string) error {
			capturedOptions = options
			capturedTag = tag
			options.Log.Info("release asset verified", "asset", "agent-gate_darwin_amd64.tar.gz", "tag", tag)
			options.Log.Info("release asset verified", "asset", "agent-gate_linux_arm64.tar.gz", "tag", tag)
			return nil
		},
	)

	if exitCode != 0 {
		t.Fatalf("runVerifyRelease() = %d, want 0\nstderr:\n%s", exitCode, stderr.String())
	}
	if capturedTag != "v1.2.3" {
		t.Fatalf("tag = %q, want v1.2.3", capturedTag)
	}
	if capturedOptions.Config.Repo != "agoodkind/agent-gate" {
		t.Fatalf("Repo = %q", capturedOptions.Config.Repo)
	}
	if capturedOptions.Config.Binary != "agent-gate" {
		t.Fatalf("Binary = %q", capturedOptions.Config.Binary)
	}
	if capturedOptions.Config.APIBaseURL != "https://api.example.invalid" {
		t.Fatalf("APIBaseURL = %q", capturedOptions.Config.APIBaseURL)
	}
	if capturedOptions.Config.AuthToken != "test-token" {
		t.Fatalf("AuthToken = %q", capturedOptions.Config.AuthToken)
	}
	if stdout.String() != "verified 2 assets for v1.2.3\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunVerifyReleaseRequiresFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runVerifyRelease(
		context.Background(),
		[]string{"--tag", "v1.2.3", "--binary", "agent-gate"},
		&stdout,
		&stderr,
		func(_ context.Context, _ selfupdate.Options, _ string) error {
			return errors.New("should not run verifier")
		},
	)

	if exitCode != 1 {
		t.Fatalf("runVerifyRelease() = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "--repo is required") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
