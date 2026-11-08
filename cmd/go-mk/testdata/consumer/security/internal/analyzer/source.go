package analyzer

import (
	"context"
	"os"
	"os/exec"
)

func InstallCommand() *exec.Cmd {
	installSpec := os.Getenv("STATICCHECK_EXTRA_INSTALL")
	command := exec.CommandContext(context.Background(), "go", "install", installSpec)

	return command
}
