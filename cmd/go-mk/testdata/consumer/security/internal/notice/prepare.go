package notice

import (
	"context"
	"os"
	"os/exec"
)

func PrepareCommand() *exec.Cmd {
	args := []string{"-f", os.Getenv("GO_MK_NOTICE_MAKEFILE")}
	command := exec.CommandContext(context.Background(), "make", args...)

	return command
}
