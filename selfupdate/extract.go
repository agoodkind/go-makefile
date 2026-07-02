package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func extractCandidate(archivePath string, binary string) (string, func(), error) {
	slog.Info("update extract candidate", "archive", archivePath)
	tmpDir, err := os.MkdirTemp("", binary+"-update-*")
	if err != nil {
		slog.Warn("update extract dir create failed", "archive", archivePath, "err", err)
		return "", func() {}, fmt.Errorf("create extract dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	file, err := os.Open(archivePath)
	if err != nil {
		cleanup()
		slog.Warn("update archive open failed", "archive", archivePath, "err", err)
		return "", cleanup, fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		cleanup()
		slog.Warn("update gzip reader open failed", "archive", archivePath, "err", err)
		return "", cleanup, fmt.Errorf("open gzip archive: %w", err)
	}
	defer func() { _ = gzipReader.Close() }()
	tarReader := tar.NewReader(gzipReader)
	candidatePath := filepath.Join(tmpDir, binary)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			slog.Warn("update archive read failed", "archive", archivePath, "err", err)
			return "", cleanup, fmt.Errorf("read archive: %w", err)
		}
		if header.Name != binary {
			continue
		}
		if header.Size <= 0 || header.Size > maxExtractedBinaryBytes {
			cleanup()
			sizeErr := fmt.Errorf("candidate size %d outside allowed range", header.Size)
			slog.Warn("update candidate size rejected", "archive", archivePath, "size", header.Size, "err", sizeErr)
			return "", cleanup, sizeErr
		}
		out, err := os.OpenFile(candidatePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			cleanup()
			slog.Warn("update candidate create failed", "path", candidatePath, "err", err)
			return "", cleanup, fmt.Errorf("create candidate: %w", err)
		}
		_, copyErr := io.CopyN(out, tarReader, header.Size)
		closeErr := out.Close()
		if copyErr != nil {
			cleanup()
			slog.Warn("update candidate write failed", "path", candidatePath, "err", copyErr)
			return "", cleanup, fmt.Errorf("write candidate: %w", copyErr)
		}
		if closeErr != nil {
			cleanup()
			slog.Warn("update candidate close failed", "path", candidatePath, "err", closeErr)
			return "", cleanup, fmt.Errorf("close candidate: %w", closeErr)
		}
		return candidatePath, cleanup, nil
	}
	cleanup()
	err = fmt.Errorf("archive did not contain %s", binary)
	slog.Warn("update candidate missing", "archive", archivePath, "err", err)
	return "", cleanup, err
}

func validateCandidate(ctx context.Context, cfg Config, candidatePath string) error {
	slog.InfoContext(ctx, "update validate candidate", "path", candidatePath)
	validateArgs := cfg.validateArgs()
	cmd := exec.CommandContext(ctx, candidatePath, validateArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.WarnContext(ctx, "update candidate version failed", "path", candidatePath, "err", err)
		return fmt.Errorf("candidate version failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	validateMatch := cfg.validateMatch()
	if !strings.Contains(string(output), validateMatch) {
		err := fmt.Errorf("candidate version output did not include %s", validateMatch)
		slog.WarnContext(ctx, "update candidate version output invalid", "path", candidatePath, "err", err)
		return err
	}
	if runtime.GOOS == "darwin" {
		if err := verifyDarwinCodeSignature(ctx, candidatePath); err != nil {
			return err
		}
	}
	return nil
}

func verifyDarwinCodeSignature(ctx context.Context, candidatePath string) error {
	cmd := exec.CommandContext(ctx, "codesign", "--verify", "--strict", "--verbose=2", candidatePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.WarnContext(ctx, "update candidate codesign verify failed", "path", candidatePath, "err", err)
		return fmt.Errorf("candidate codesign verify failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func replaceBinary(candidatePath string, installPath string) error {
	slog.Info("update replace binary", "candidate", candidatePath, "install_path", installPath)
	if installPath == "" {
		err := fmt.Errorf("install path is empty")
		slog.Warn("update replace binary missing install path", "err", err)
		return err
	}
	targetDir := filepath.Dir(installPath)
	tmpPrefix := "." + filepath.Base(installPath) + "-update-"
	tmpPath := filepath.Join(targetDir, tmpPrefix+strconv.FormatInt(timeNow().UnixNano(), 10))
	in, err := os.Open(candidatePath)
	if err != nil {
		slog.Warn("update candidate open failed", "path", candidatePath, "err", err)
		return fmt.Errorf("open candidate: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		slog.Warn("update install temp create failed", "path", tmpPath, "err", err)
		return fmt.Errorf("create install temp: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		slog.Warn("update install temp write failed", "path", tmpPath, "err", copyErr)
		return fmt.Errorf("write install temp: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		slog.Warn("update install temp close failed", "path", tmpPath, "err", closeErr)
		return fmt.Errorf("close install temp: %w", closeErr)
	}
	if err := os.Rename(tmpPath, installPath); err != nil {
		_ = os.Remove(tmpPath)
		slog.Warn("update install replace failed", "path", installPath, "err", err)
		return fmt.Errorf("replace installed binary: %w", err)
	}
	return nil
}
