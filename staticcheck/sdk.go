package staticcheck

import (
	"bytes"
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
	return resolveCommandEntryBuildContext(build.Default, installedGoRoot)
}

func resolveCommandEntryBuildContext(
	buildContext build.Context,
	lookupRoot func() (string, error),
) (build.Context, error) {
	if buildContext.GOROOT != "" && commandEntrySDKAvailable(buildContext) {
		return buildContext, nil
	}

	slog.Debug("resolve installed Go SDK for commandentry")
	root, err := lookupRoot()
	if err != nil {
		failure := errors.Join(errCommandEntrySDK, err)
		slog.Error("resolve commandentry Go SDK failed", "err", failure)

		return buildContext, failure
	}

	buildContext.GOROOT = strings.TrimSpace(root)
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

func installedGoRoot() (string, error) {
	command := exec.CommandContext(context.Background(), "go", "env", "GOROOT")
	var stderr bytes.Buffer
	command.Stderr = &stderr

	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("go env GOROOT: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return string(output), nil
}

func commandEntrySDKAvailable(buildContext build.Context) bool {
	metadata, err := buildContext.Import("fmt", "", build.FindOnly)

	return err == nil && metadata.Goroot
}
