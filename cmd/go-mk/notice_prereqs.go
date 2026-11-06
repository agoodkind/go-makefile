package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

// Copy configuration and scripts into the temporary checkout. Keep its output
// files separate from the consumer's current build inputs and tool output.
func stageNoticeTools(source, destination string) error {
	slog.Info("notice stage tools", slog.String("source", source), slog.String("destination", destination))
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "scripts" && entry.IsDir() {
			if err := os.CopyFS(filepath.Join(destination, name), os.DirFS(filepath.Join(source, name))); err != nil {
				return err
			}
			continue
		}
		if entry.IsDir() {
			continue
		}
		if name == "staticcheck-extra" {
			if err := os.Symlink(filepath.Join(source, name), filepath.Join(destination, name)); err != nil {
				return err
			}
			continue
		}
		extension := filepath.Ext(name)
		if extension != ".mk" && extension != ".yml" && extension != ".yaml" && extension != ".txt" && name != ".go-mk-commit" {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, name), contents, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func prepareNoticeHistory() error {
	slog.Info("notice prepare historical source")
	engine, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"--no-print-directory", "GO_MK_BIN=" + engine, "go-mk-workspace", "go-mk-cgo-deps"}
	command := exec.Command("make", args...)
	environment := os.Environ()
	for _, name := range []string{"MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES"} {
		environment = setEnvVar(environment, name, "")
	}
	command.Env = environment
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		wrapped := fmt.Errorf("prepare source before the notice effective date: %w", err)
		slog.Error("notice prepare historical source", slog.String("err", wrapped.Error()))
		return wrapped
	}
	return nil
}
