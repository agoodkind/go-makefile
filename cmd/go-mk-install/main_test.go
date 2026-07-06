package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"goodkind.io/go-makefile/selfupdate"
)

func TestRunInstallerInstallsRequestedReleaseAndExecsPostInstallArgs(t *testing.T) {
	restoreInstallerSeams(t)
	var installOptions selfupdate.InstallReleaseBinaryOptions
	var execPath string
	var execArgs []string
	installReleaseBinaryFunc = func(
		ctx context.Context,
		options selfupdate.InstallReleaseBinaryOptions,
	) (selfupdate.InstallReleaseBinaryResult, error) {
		if ctx.Err() != context.Canceled {
			t.Fatalf("install context error = %v, want canceled", ctx.Err())
		}
		installOptions = options
		return selfupdate.InstallReleaseBinaryResult{
			Tag:         "v1.2.3",
			AssetName:   "agent-gate_darwin_arm64.tar.gz",
			InstallPath: filepath.Join(options.BinDir, options.Options.Config.Binary),
		}, nil
	}
	execInstalledBinaryFunc = func(path string, args []string) error {
		execPath = path
		execArgs = append([]string{}, args...)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runInstaller(
		ctx,
		[]string{
			"--repo", "agoodkind/agent-gate",
			"--binary", "agent-gate",
			"--bin-dir", "/tmp/bin",
			"--version", "v1.2.3",
			"--",
			"configure",
			"--force",
		},
		&stdout,
		&stderr,
	)

	if exitCode != 0 {
		t.Fatalf("runInstaller() = %d, want 0\nstderr:\n%s", exitCode, stderr.String())
	}
	if installOptions.Options.Config.Repo != "agoodkind/agent-gate" {
		t.Fatalf("install repo = %q", installOptions.Options.Config.Repo)
	}
	if installOptions.Options.Config.Binary != "agent-gate" {
		t.Fatalf("install binary = %q", installOptions.Options.Config.Binary)
	}
	if installOptions.Version != "v1.2.3" {
		t.Fatalf("install version = %q", installOptions.Version)
	}
	if installOptions.Channel != selfupdate.ReleaseChannelRolling {
		t.Fatalf("install channel = %q, want rolling", installOptions.Channel)
	}
	if installOptions.BinDir != "/tmp/bin" {
		t.Fatalf("install bin dir = %q", installOptions.BinDir)
	}
	if execPath != "/tmp/bin/agent-gate" {
		t.Fatalf("exec path = %q", execPath)
	}
	if len(execArgs) != 2 || execArgs[0] != "configure" || execArgs[1] != "--force" {
		t.Fatalf("exec args = %#v", execArgs)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("installed: /tmp/bin/agent-gate\n")) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunInstallerRejectsRemovedRequireAttestationFlag(t *testing.T) {
	restoreInstallerSeams(t)
	installReleaseBinaryFunc = func(
		_ context.Context,
		_ selfupdate.InstallReleaseBinaryOptions,
	) (selfupdate.InstallReleaseBinaryResult, error) {
		t.Fatal("installReleaseBinaryFunc called for invalid flags")
		return selfupdate.InstallReleaseBinaryResult{}, nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runInstaller(
		context.Background(),
		[]string{
			"--repo", "agoodkind/agent-gate",
			"--binary", "agent-gate",
			"--require-attestation",
		},
		&stdout,
		&stderr,
	)

	if exitCode != 1 {
		t.Fatalf("runInstaller() = %d, want 1", exitCode)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("flag provided but not defined: -require-attestation")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunInstallerRequiresRepoAndBinary(t *testing.T) {
	restoreInstallerSeams(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runInstaller(context.Background(), []string{"--repo", "agoodkind/agent-gate"}, &stdout, &stderr)

	if exitCode != 1 {
		t.Fatalf("runInstaller() = %d, want 1", exitCode)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("--binary is required")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func restoreInstallerSeams(t *testing.T) {
	t.Helper()
	originalInstallReleaseBinaryFunc := installReleaseBinaryFunc
	originalExecInstalledBinaryFunc := execInstalledBinaryFunc
	t.Cleanup(func() {
		installReleaseBinaryFunc = originalInstallReleaseBinaryFunc
		execInstalledBinaryFunc = originalExecInstalledBinaryFunc
	})
}
