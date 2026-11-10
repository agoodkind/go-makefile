package history

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
)

// UpgradeEnvironment selects the committed source used by an upgrade notice.
const UpgradeEnvironment = "_GO_MK_NOTICE_UPGRADE"

// FormatSource applies the current engine's formatters before notice analysis.
//
// Upgrade notices analyze both the consumer and its isolated committed copy
// with the same layout. Formatting failures prevent baseline acceptance.
//
// The notice runner supplies its active context:
//
//	FormatSource(ctx)
func FormatSource(ctx context.Context) error {
	slog.InfoContext(ctx, "notice format source")
	engine, err := os.Executable()
	if err != nil {
		return failed("resolve notice formatter executable", err)
	}

	command := exec.CommandContext(ctx, engine, "fmt")
	command.Env = os.Environ()
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return failed("format source for upgrade notice", errors.Join(ctx.Err(), err))
	}

	return nil
}
