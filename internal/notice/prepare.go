// Package notice prepares historical Go modules for notice analysis.
package notice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

var moduleMakefiles = map[string]string{
	"goodkind.io/go-makefile":             "go.mk",
	"goodkind.io/go-makefile/staticcheck": "../go.mk",
}

var (
	errMissingModuleDirective = errors.New("historical go.mod requires a module directive")
	errMakefileType           = errors.New("historical preparation requires a regular Makefile")
)

func prepareHistory(engine string) error {
	slog.Info("notice prepare historical source")
	contents, err := os.ReadFile("go.mod")
	if err != nil {
		return fmt.Errorf("read historical go.mod: %w", err)
	}

	module, err := modfile.ParseLax("go.mod", contents, nil)
	if err != nil {
		return fmt.Errorf("parse historical go.mod: %w", err)
	}

	if module.Module == nil {
		return errMissingModuleDirective
	}

	args := []string{"--no-print-directory"}
	makefile := os.Getenv("GO_MK_NOTICE_MAKEFILE")
	if makefile == "" {
		makefile = moduleMakefiles[module.Module.Mod.Path]
	}

	if makefile != "" {
		makefile, err = preparationMakefile(makefile)
		if err != nil {
			return err
		}

		args = append(args, "-f", makefile)
	}

	args = append(args, "GO_MK_BIN="+engine, "go-mk-workspace", "go-mk-cgo-deps")
	// #nosec G702 -- preparationMakefile resolves the required regular file.
	command := exec.CommandContext(context.Background(), "make", args...)
	command.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKEOVERRIDES=")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		wrapped := fmt.Errorf("prepare source before the notice effective date: %w", err)
		slog.Error("notice prepare historical source", slog.String("err", wrapped.Error()))

		return wrapped
	}

	return nil
}

func preparationMakefile(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", failure("resolve historical Makefile", err)
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", failure("resolve required historical Makefile", err)
	}

	directory, err := os.OpenRoot(filepath.Dir(resolved))
	if err != nil {
		return "", failure("open historical Makefile directory", err)
	}

	defer func() { _ = directory.Close() }()

	info, err := directory.Stat(filepath.Base(resolved))
	if err != nil {
		return "", failure("inspect required historical Makefile", err)
	}

	if !info.Mode().IsRegular() {
		return "", inputFailure(errMakefileType, "", ": "+resolved)
	}

	return resolved, nil
}
