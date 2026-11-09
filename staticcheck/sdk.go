package staticcheck

import (
	"context"
	"errors"
	"fmt"
	"go/build"
	"log/slog"
	"os/exec"
	"strings"
)

var errCommandEntrySDK = errors.New(
	"commandentry requires Go standard-library sources; install Go on PATH or set GOROOT",
)

func commandEntryBuildContext() (build.Context, error) {
	buildContext := build.Default
	if buildContext.GOROOT != "" && commandEntrySDKAvailable(buildContext) {
		return buildContext, nil
	}

	slog.Debug("resolve installed Go SDK for commandentry")
	command := exec.CommandContext(context.Background(), "go", "env", "GOROOT")
	output, err := command.CombinedOutput()
	if err != nil {
		failure := fmt.Errorf(
			"%w: go env GOROOT: %w: %s",
			errCommandEntrySDK,
			err,
			strings.TrimSpace(string(output)),
		)
		slog.Error("resolve commandentry Go SDK failed", "err", failure)

		return buildContext, failure
	}

	buildContext.GOROOT = strings.TrimSpace(string(output))
	if buildContext.GOROOT == "" {
		slog.Error("commandentry Go SDK root is empty", "err", errCommandEntrySDK)

		return buildContext, errCommandEntrySDK
	}

	metadata, err := buildContext.Import("fmt", "", build.FindOnly)
	if err != nil || !metadata.Goroot {
		failure := errors.Join(errCommandEntrySDK, err)
		slog.Error(
			"commandentry Go SDK standard library is unavailable",
			"goroot",
			buildContext.GOROOT,
			"err",
			failure,
		)

		return buildContext, failure
	}

	return buildContext, nil
}

func commandEntrySDKAvailable(buildContext build.Context) bool {
	metadata, err := buildContext.Import("fmt", "", build.FindOnly)

	return err == nil && metadata.Goroot
}
