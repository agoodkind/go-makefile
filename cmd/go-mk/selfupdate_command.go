package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"goodkind.io/go-makefile/internal/version"
	"goodkind.io/go-makefile/selfupdate"
)

const (
	goMkSelfUpdateRepo        = "agoodkind/go-makefile"
	goMkSelfUpdateBinary      = "go-mk"
	goMkSelfUpdateLaunchd     = "io.goodkind.go-mk.selfupdate"
	goMkSelfUpdateSystemdUnit = "go-mk-selfupdate.service"
)

type selfUpdateCommandOptions struct {
	check  bool
	dryRun bool
	daemon bool
}

type selfUpdateInstallServiceOptions struct {
	programPath string
	label       string
	plistPath   string
	logPath     string
	unit        string
	unitPath    string
	restartSec  string
	env         []string
}

type selfUpdateServicePlatform string

const (
	selfUpdateServicePlatformDarwin selfUpdateServicePlatform = "darwin"
	selfUpdateServicePlatformLinux  selfUpdateServicePlatform = "linux"
)

var (
	runSelfUpdateCommandFunc            = selfupdate.RunUpdateCommand
	runSelfUpdateSchedulerFunc          = selfupdate.RunScheduler
	installLaunchdSelfUpdateServiceFunc = selfupdate.InstallLaunchdService
	installSystemdSelfUpdateServiceFunc = selfupdate.InstallSystemdUserService
	currentExecutableHashFunc           = runningExecutableHash
	currentExecutablePathFunc           = os.Executable
	currentSelfUpdateUserHomeDirFunc    = os.UserHomeDir
)

func registerSelfUpdateCommand(root *cobra.Command) {
	options := selfUpdateCommandOptions{}
	command := &cobra.Command{
		Use:   "selfupdate",
		Short: "Update go-mk from the go-makefile release stream",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if options.daemon {
				runGoMkSelfUpdateScheduler(command.Context())
				recordedExit = 0
				return nil
			}
			recordedExit = runSelfUpdateCommandFunc(
				command.Context(),
				goMkSelfUpdateOptions(),
				selfUpdateCommandArgs(options),
				os.Stdout,
				os.Stderr,
			)
			return nil
		},
	}
	command.Flags().BoolVar(&options.check, "check", false, "check for a go-mk update")
	command.Flags().BoolVar(&options.dryRun, "dry-run", false, "stage and verify without replacing go-mk")
	command.Flags().BoolVar(&options.daemon, "daemon", false, "run the background self-update scheduler")
	command.AddCommand(newSelfUpdateWatchCommand())
	command.AddCommand(newSelfUpdateInstallServiceCommand())
	root.AddCommand(command)
}

func newSelfUpdateWatchCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Run the go-mk self-update scheduler",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			runGoMkSelfUpdateScheduler(command.Context())
			recordedExit = 0
			return nil
		},
	}
}

func newSelfUpdateInstallServiceCommand() *cobra.Command {
	options := selfUpdateInstallServiceOptions{}
	command := &cobra.Command{
		Use:   "install-service",
		Short: "Install the go-mk self-update scheduler as a user service",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			recordedExit = statusFromError(runSelfUpdateInstallServiceCommand(options, runtime.GOOS))
			return nil
		},
	}
	command.Flags().StringVar(&options.programPath, "program", "", "go-mk executable path")
	command.Flags().StringVar(&options.label, "label", "", "launchd label")
	command.Flags().StringVar(&options.plistPath, "plist-path", "", "launchd plist path")
	command.Flags().StringVar(&options.logPath, "log-path", "", "launchd log path")
	command.Flags().StringVar(&options.unit, "unit", "", "systemd user unit name")
	command.Flags().StringVar(&options.unitPath, "unit-path", "", "systemd user unit path")
	command.Flags().StringVar(&options.restartSec, "restart-sec", "", "systemd RestartSec value")
	command.Flags().StringArrayVar(&options.env, "env", nil, "service environment KEY=VALUE")
	return command
}

func selfUpdateCommandArgs(options selfUpdateCommandOptions) []string {
	args := []string{}
	if options.check {
		args = append(args, "--check")
	}
	if options.dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

func runGoMkSelfUpdateScheduler(ctx context.Context) {
	runSelfUpdateSchedulerFunc(ctx, selfupdate.SchedulerHooks{
		Enabled: func() bool {
			return !envFalse("GO_MK_SELFUPDATE_ENABLED")
		},
		Mode: func() string {
			mode := strings.TrimSpace(os.Getenv("GO_MK_SELFUPDATE_MODE"))
			if mode == "" {
				return selfupdate.ModeApply
			}
			return mode
		},
		Options: func() selfupdate.Options {
			return goMkSelfUpdateOptions()
		},
		StopForRelaunch: func() {
			slog.InfoContext(ctx, "go-mk selfupdate scheduler stopping for relaunch")
		},
		Log: slog.Default(),
	})
}

func runSelfUpdateInstallServiceCommand(options selfUpdateInstallServiceOptions, goos string) error {
	programPath, err := selfUpdateProgramPath(options.programPath)
	if err != nil {
		return err
	}
	arguments := []string{"selfupdate", "watch"}
	environment, err := selfupdate.ParseEnvironmentPairs(options.env)
	if err != nil {
		return err
	}
	switch selfUpdateServicePlatform(goos) {
	case selfUpdateServicePlatformDarwin:
		plistPath, logPath, pathErr := selfUpdateLaunchdPaths(options)
		if pathErr != nil {
			return pathErr
		}
		label := options.label
		if label == "" {
			label = goMkSelfUpdateLaunchd
		}
		return installLaunchdSelfUpdateServiceFunc(selfupdate.LaunchdServiceOptions{
			Label:       label,
			ProgramPath: programPath,
			Arguments:   arguments,
			PlistPath:   plistPath,
			LogPath:     logPath,
			Environment: environment,
			RunAtLoad:   true,
			KeepAlive:   true,
			Stdout:      os.Stdout,
		})
	case selfUpdateServicePlatformLinux:
		unit, unitPath, pathErr := selfUpdateSystemdPaths(options)
		if pathErr != nil {
			return pathErr
		}
		restartSec := options.restartSec
		if restartSec == "" {
			restartSec = "30"
		}
		return installSystemdSelfUpdateServiceFunc(selfupdate.SystemdUserServiceOptions{
			Unit:        unit,
			ProgramPath: programPath,
			Arguments:   arguments,
			UnitPath:    unitPath,
			Description: "go-mk self-update scheduler",
			Restart:     "always",
			RestartSec:  restartSec,
			Environment: environment,
			Stdout:      os.Stdout,
		})
	default:
		return fmt.Errorf("selfupdate install-service is unsupported on %s", goos)
	}
}

func selfUpdateProgramPath(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return configured, nil
	}
	path, err := currentExecutablePathFunc()
	if err != nil {
		slog.Warn("go-mk selfupdate executable path resolve failed", "err", err)
		return "", fmt.Errorf("resolve go-mk executable: %w", err)
	}
	return path, nil
}

func selfUpdateLaunchdPaths(options selfUpdateInstallServiceOptions) (string, string, error) {
	home, err := currentSelfUpdateUserHomeDirFunc()
	if err != nil {
		slog.Warn("go-mk selfupdate launchd home resolve failed", "err", err)
		return "", "", fmt.Errorf("resolve home directory: %w", err)
	}
	plistPath := options.plistPath
	if plistPath == "" {
		plistPath = filepath.Join(home, "Library", "LaunchAgents", goMkSelfUpdateLaunchd+".plist")
	}
	logPath := options.logPath
	if logPath == "" {
		logPath = filepath.Join(home, "Library", "Logs", "go-mk-selfupdate.log")
	}
	return plistPath, logPath, nil
}

func selfUpdateSystemdPaths(options selfUpdateInstallServiceOptions) (string, string, error) {
	home, err := currentSelfUpdateUserHomeDirFunc()
	if err != nil {
		slog.Warn("go-mk selfupdate systemd home resolve failed", "err", err)
		return "", "", fmt.Errorf("resolve home directory: %w", err)
	}
	unit := options.unit
	if unit == "" {
		unit = goMkSelfUpdateSystemdUnit
	}
	unitPath := options.unitPath
	if unitPath == "" {
		unitPath = filepath.Join(home, ".config", "systemd", "user", unit)
	}
	return unit, unitPath, nil
}

func goMkSelfUpdateOptions() selfupdate.Options {
	return selfupdate.Options{
		Config: selfupdate.Config{
			Repo:             goMkSelfUpdateRepo,
			Binary:           goMkSelfUpdateBinary,
			CurrentVersion:   defaultedBuildInfo(version.Version, "dev"),
			CurrentCommit:    defaultedBuildInfo(version.Commit, "unknown"),
			CurrentBuildHash: currentGoMkBuildHash(),
			CurrentDirty:     currentGoMkDirty(),
			ValidateArgs:     []string{"version"},
			ValidateMatch:    "version:",
			AuthToken:        selfUpdateAuthToken(),
		},
	}
}

func currentGoMkBuildHash() string {
	if hash := strings.TrimSpace(version.BinHash); hash != "" {
		return hash
	}
	executableHash, err := currentExecutableHashFunc()
	if err == nil && executableHash.digest != "" {
		return executableHash.digest
	}
	if buildTime := strings.TrimSpace(version.BuildTime); buildTime != "" {
		return buildTime
	}
	return "unknown"
}

func currentGoMkDirty() bool {
	dirty, err := strconv.ParseBool(strings.TrimSpace(version.Dirty))
	if err == nil {
		return dirty
	}
	return version.Version == "dev" || version.Version == "unknown"
}

func defaultedBuildInfo(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func selfUpdateAuthToken() string {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}

func envFalse(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "0" || value == "false" || value == "no" || value == "off"
}

func runVersion() int {
	fmt.Fprintf(os.Stdout, "version: %s\n", defaultedBuildInfo(version.Version, "dev"))
	fmt.Fprintf(os.Stdout, "commit: %s\n", defaultedBuildInfo(version.Commit, "unknown"))
	fmt.Fprintf(os.Stdout, "dirty: %t\n", currentGoMkDirty())
	fmt.Fprintf(os.Stdout, "build_hash: %s\n", currentGoMkBuildHash())
	return 0
}
