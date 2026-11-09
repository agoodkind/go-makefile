package history

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

func stageTools(source, destination string) error {
	slog.Info(
		"notice stage tools",
		slog.String("source", source),
		slog.String("destination", destination),
	)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return failed("create historical notice tools directory", err)
	}

	directory, err := os.OpenRoot(destination)
	if err != nil {
		return failed("open historical notice tools directory", err)
	}

	defer func() {
		if err := directory.Close(); err != nil {
			slog.Error("notice close staged tools directory", slog.Any("error", err))
		}
	}()

	entries, err := os.ReadDir(source)
	if err != nil {
		return failed("read current notice tools", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "scripts" && entry.IsDir() {
			if err := os.CopyFS(
				filepath.Join(destination, name),
				os.DirFS(filepath.Join(source, name)),
			); err != nil {
				return failed("copy historical notice scripts", err)
			}

			continue
		}

		if entry.IsDir() {
			continue
		}

		if name == "staticcheck-extra" {
			if err := os.Symlink(
				filepath.Join(source, name),
				filepath.Join(destination, name),
			); err != nil {
				return failed("link historical notice analyzer", err)
			}

			continue
		}

		extension := filepath.Ext(name)
		if extension != ".mk" && extension != ".yml" && extension != ".yaml" &&
			extension != ".txt" &&
			name != ".go-mk-commit" {
			continue
		}

		contents, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			return failed("read notice tool configuration", err)
		}

		if err := directory.WriteFile(name, contents, 0o644); err != nil {
			return failed("write historical notice tool configuration", err)
		}
	}

	return nil
}

func prepareSource(ctx context.Context) error {
	slog.Info("notice prepare historical source")
	engine, err := os.Executable()
	if err != nil {
		return failed("resolve notice engine executable", err)
	}

	arguments := []string{"--no-print-directory"}
	if makefile := os.Getenv("GO_MK_NOTICE_MAKEFILE"); makefile != "" {
		makefile, err = preparationMakefile(makefile)
		if err != nil {
			return err
		}

		arguments = append(arguments, "-f", makefile)
	}

	arguments = append(arguments, "GO_MK_BIN="+engine, "go-mk-workspace", "go-mk-cgo-deps")
	// #nosec G702 -- preparationMakefile resolves the required regular file.
	command := exec.CommandContext(ctx, "make", arguments...)
	command.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKEOVERRIDES=")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return failed(
			"prepare source before the notice effective date",
			errors.Join(ctx.Err(), err),
		)
	}

	return nil
}
