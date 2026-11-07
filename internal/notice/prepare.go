// Package notice prepares historical Go modules for notice analysis.
package notice

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"golang.org/x/mod/modfile"
)

var moduleMakefiles = map[string]string{
	"goodkind.io/go-makefile":             "go.mk",
	"goodkind.io/go-makefile/staticcheck": "../go.mk",
}

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
		return fmt.Errorf("historical go.mod requires a module directive")
	}

	args := []string{"--no-print-directory"}
	if makefile, ownModule := moduleMakefiles[module.Module.Mod.Path]; ownModule {
		args = append(args, "-f", makefile)
	}

	args = append(args, "GO_MK_BIN="+engine, "go-mk-workspace", "go-mk-cgo-deps")
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
