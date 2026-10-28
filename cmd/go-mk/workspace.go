package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
)

func registerWorkspaceCommand(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "workspace-toolchain",
		Short: "Apply the module toolchain preference to the local workspace",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			recordedExit = statusFromError(runWorkspaceToolchain(command.Context()))
			return nil
		},
	})
}

func runWorkspaceToolchain(ctx context.Context) error {
	workspacePath, err := filepath.Abs("go.work")
	if err != nil {
		slog.Error("workspace resolve local path failed", slog.Any("err", err))
		return fmt.Errorf("resolve local workspace: %w", err)
	}
	selected := strings.TrimSpace(os.Getenv("GOWORK"))
	if selected != "" && selected != workspacePath {
		return nil
	}
	workspaceData, err := os.ReadFile(workspacePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		slog.Error("workspace read failed", slog.Any("err", err))
		return fmt.Errorf("read local workspace: %w", err)
	}
	moduleData, err := os.ReadFile("go.mod")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		slog.Error("workspace read module failed", slog.Any("err", err))
		return fmt.Errorf("read workspace module: %w", err)
	}
	module, err := modfile.Parse("go.mod", moduleData, nil)
	if err != nil {
		slog.Error("workspace parse module failed", slog.Any("err", err))
		return fmt.Errorf("parse workspace module: %w", err)
	}
	if module.Toolchain == nil {
		return nil
	}
	workspace, err := modfile.ParseWork(workspacePath, workspaceData, nil)
	if err != nil {
		slog.Error("workspace parse failed", slog.Any("err", err))
		return fmt.Errorf("parse local workspace: %w", err)
	}
	preferred := module.Toolchain.Name
	if workspace.Toolchain != nil && workspace.Toolchain.Name == preferred {
		return nil
	}
	slog.Info("workspace update preferred toolchain", slog.String("toolchain", preferred))
	command := exec.CommandContext(ctx, "go", "work", "edit")
	command.Args = append(command.Args, "-toolchain="+preferred)
	command.Env = setEnvVar(os.Environ(), "GOWORK", workspacePath)
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		slog.Error("workspace update toolchain failed", slog.Any("err", commandErr))
		return fmt.Errorf("update workspace toolchain: %w\n%s", commandErr, output)
	}
	return nil
}
