package main

import (
	"context"
	"io"
	"testing"

	"goodkind.io/go-makefile/selfupdate"
)

func TestSelfUpdateCommandRunsOneShotWithGoMkIdentity(t *testing.T) {
	restoreSelfUpdateCommandSeams(t)
	var gotOptions selfupdate.Options
	var gotArgs []string
	runSelfUpdateCommandFunc = func(
		_ context.Context,
		options selfupdate.Options,
		args []string,
		_ io.Writer,
		_ io.Writer,
	) int {
		gotOptions = options
		gotArgs = append([]string{}, args...)
		return 0
	}

	root := newRootCommand()
	root.SetArgs([]string{"selfupdate", "--check"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error: %v", err)
	}

	if recordedExit != 0 {
		t.Fatalf("recordedExit = %d, want 0", recordedExit)
	}
	if gotOptions.Config.Repo != "agoodkind/go-makefile" {
		t.Fatalf("Repo = %q", gotOptions.Config.Repo)
	}
	if gotOptions.Config.Binary != "go-mk" {
		t.Fatalf("Binary = %q", gotOptions.Config.Binary)
	}
	if gotOptions.Config.CurrentVersion == "" {
		t.Fatal("CurrentVersion is empty")
	}
	if gotOptions.Config.CurrentCommit == "" {
		t.Fatal("CurrentCommit is empty")
	}
	if gotOptions.Config.CurrentBuildHash == "" {
		t.Fatal("CurrentBuildHash is empty")
	}
	if len(gotArgs) != 1 || gotArgs[0] != "--check" {
		t.Fatalf("args = %#v, want --check", gotArgs)
	}
}

func TestSelfUpdateWatchRunsSchedulerInApplyMode(t *testing.T) {
	restoreSelfUpdateCommandSeams(t)
	var gotHooks selfupdate.SchedulerHooks
	runSelfUpdateSchedulerFunc = func(_ context.Context, hooks selfupdate.SchedulerHooks) {
		gotHooks = hooks
	}

	root := newRootCommand()
	root.SetArgs([]string{"selfupdate", "watch"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error: %v", err)
	}

	if recordedExit != 0 {
		t.Fatalf("recordedExit = %d, want 0", recordedExit)
	}
	if gotHooks.Enabled == nil || !gotHooks.Enabled() {
		t.Fatal("scheduler Enabled hook is missing or false")
	}
	if gotHooks.Mode == nil || gotHooks.Mode() != selfupdate.ModeApply {
		t.Fatalf("scheduler mode = %q, want apply", gotHooks.Mode())
	}
	if gotHooks.Options == nil {
		t.Fatal("scheduler Options hook is missing")
	}
	if gotHooks.Options().Config.Binary != "go-mk" {
		t.Fatalf("scheduler binary = %q", gotHooks.Options().Config.Binary)
	}
}

func TestSelfUpdateInstallServiceRendersLaunchdWatchService(t *testing.T) {
	restoreSelfUpdateCommandSeams(t)
	var got selfupdate.LaunchdServiceOptions
	installLaunchdSelfUpdateServiceFunc = func(options selfupdate.LaunchdServiceOptions) error {
		got = options
		return nil
	}

	err := runSelfUpdateInstallServiceCommand(selfUpdateInstallServiceOptions{
		programPath: "/usr/local/bin/go-mk",
		plistPath:   "/tmp/io.goodkind.go-mk.selfupdate.plist",
		logPath:     "/tmp/go-mk-selfupdate.log",
		env:         []string{"GO_MK_SELFUPDATE_MODE=check"},
	}, "darwin")

	if err != nil {
		t.Fatalf("runSelfUpdateInstallServiceCommand() error: %v", err)
	}
	if got.Label != "io.goodkind.go-mk.selfupdate" {
		t.Fatalf("Label = %q", got.Label)
	}
	if got.ProgramPath != "/usr/local/bin/go-mk" {
		t.Fatalf("ProgramPath = %q", got.ProgramPath)
	}
	if len(got.Arguments) != 2 || got.Arguments[0] != "selfupdate" || got.Arguments[1] != "watch" {
		t.Fatalf("Arguments = %#v", got.Arguments)
	}
	if len(got.Environment) != 1 || got.Environment[0].Name != "GO_MK_SELFUPDATE_MODE" || got.Environment[0].Value != "check" {
		t.Fatalf("Environment = %#v", got.Environment)
	}
	if !got.RunAtLoad || !got.KeepAlive {
		t.Fatalf("RunAtLoad=%t KeepAlive=%t, want true/true", got.RunAtLoad, got.KeepAlive)
	}
}

func restoreSelfUpdateCommandSeams(t *testing.T) {
	t.Helper()
	originalRunSelfUpdateCommandFunc := runSelfUpdateCommandFunc
	originalRunSelfUpdateSchedulerFunc := runSelfUpdateSchedulerFunc
	originalInstallLaunchdSelfUpdateServiceFunc := installLaunchdSelfUpdateServiceFunc
	originalInstallSystemdSelfUpdateServiceFunc := installSystemdSelfUpdateServiceFunc
	t.Cleanup(func() {
		runSelfUpdateCommandFunc = originalRunSelfUpdateCommandFunc
		runSelfUpdateSchedulerFunc = originalRunSelfUpdateSchedulerFunc
		installLaunchdSelfUpdateServiceFunc = originalInstallLaunchdSelfUpdateServiceFunc
		installSystemdSelfUpdateServiceFunc = originalInstallSystemdSelfUpdateServiceFunc
	})
	recordedExit = 0
}
