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
	"strings"
)

// extractCandidate unpacks binary from archivePath into a hidden temporary file
// in targetDir. It refuses a member larger than maxBytes. The tar header
// supplies the member size, and the archive author controls that header. The
// caller supplies maxBytes because a plausible binary size depends on the
// consumer.
//
// The caller passes the install directory as targetDir. A candidate in that
// directory resolves @loader_path and $ORIGIN runpaths to the libraries
// installed beside the binary. installCandidate then renames the candidate
// within one filesystem.
func extractCandidate(archivePath string, binary string, maxBytes int64, targetDir string) (string, func(), error) {
	slog.Info("update extract candidate", "archive", archivePath, "dir", targetDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		slog.Warn("update candidate dir create failed", "dir", targetDir, "err", err)
		return "", func() {}, fmt.Errorf("create candidate dir: %w", err)
	}
	reserved, err := os.CreateTemp(targetDir, "."+binary+"-candidate-*")
	if err != nil {
		slog.Warn("update candidate create failed", "dir", targetDir, "err", err)
		return "", func() {}, fmt.Errorf("create candidate: %w", err)
	}
	candidatePath := reserved.Name()
	cleanup := func() { _ = os.Remove(candidatePath) }
	if err := reserved.Close(); err != nil {
		cleanup()
		slog.Warn("update candidate close failed", "path", candidatePath, "err", err)
		return "", func() {}, fmt.Errorf("close candidate: %w", err)
	}
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
		if header.Size <= 0 || header.Size > maxBytes {
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
		if err := os.Chmod(candidatePath, 0o755); err != nil {
			cleanup()
			slog.Warn("update candidate chmod failed", "path", candidatePath, "err", err)
			return "", cleanup, fmt.Errorf("chmod candidate: %w", err)
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
	if runtime.GOOS == "darwin" {
		if err := verifyDarwinCodeSignature(ctx, candidatePath); err != nil {
			return err
		}
	}
	validateArgs := cfg.validateArgs()
	cmd := exec.CommandContext(ctx, candidatePath, validateArgs...)
	if len(cfg.ValidateEnv) > 0 {
		cmd.Env = append(os.Environ(), cfg.ValidateEnv...)
	}
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

// installCandidate renames a staged candidate over installPath. The candidate
// and installPath share one directory, and one rename replaces the binary.
func installCandidate(candidatePath string, installPath string) error {
	slog.Info("update install candidate", "candidate", candidatePath, "install_path", installPath)
	if installPath == "" {
		err := fmt.Errorf("install path is empty")
		slog.Warn("update install candidate missing install path", "err", err)
		return err
	}
	if err := os.Rename(candidatePath, installPath); err != nil {
		slog.Warn("update install replace failed", "path", installPath, "err", err)
		return fmt.Errorf("replace installed binary: %w", err)
	}
	return nil
}

// backupInstalledBinary keeps the binary at installPath under a hidden name in
// the same directory and returns that name. It returns an empty name when
// nothing is installed yet. It tries a hard link first and copies the file when
// the filesystem refuses the link.
func backupInstalledBinary(installPath string) (string, error) {
	info, err := os.Lstat(installPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		slog.Warn("update installed binary stat failed", "path", installPath, "err", err)
		return "", fmt.Errorf("stat installed binary: %w", err)
	}
	targetDir := filepath.Dir(installPath)
	reserved, err := os.CreateTemp(targetDir, "."+filepath.Base(installPath)+"-previous-*")
	if err != nil {
		slog.Warn("update backup create failed", "dir", targetDir, "err", err)
		return "", fmt.Errorf("create installed binary backup: %w", err)
	}
	backupPath := reserved.Name()
	_ = reserved.Close()
	_ = os.Remove(backupPath)
	// A symlink backup is a new symlink with the same target, which a rename
	// restores as a symlink.
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(installPath)
		if err != nil {
			slog.Warn("update symlink backup read failed", "path", installPath, "err", err)
			return "", fmt.Errorf("read installed symlink: %w", err)
		}
		if err := os.Symlink(target, backupPath); err != nil {
			slog.Warn("update symlink backup create failed", "path", backupPath, "err", err)
			return "", fmt.Errorf("create installed symlink backup: %w", err)
		}
		return backupPath, nil
	}
	if linkErr := os.Link(installPath, backupPath); linkErr == nil {
		return backupPath, nil
	}
	if err := copyFile(installPath, backupPath, info.Mode().Perm()); err != nil {
		_ = os.Remove(backupPath)
		slog.Warn("update backup copy failed", "path", installPath, "err", err)
		return "", fmt.Errorf("copy installed binary backup: %w", err)
	}
	return backupPath, nil
}

func copyFile(sourcePath string, destinationPath string, mode os.FileMode) error {
	in, err := os.Open(sourcePath)
	if err != nil {
		slog.Warn("update copy source open failed", "path", sourcePath, "err", err)
		return fmt.Errorf("open %s: %w", sourcePath, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		slog.Warn("update copy destination create failed", "path", destinationPath, "err", err)
		return fmt.Errorf("create %s: %w", destinationPath, err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		slog.Warn("update copy write failed", "path", destinationPath, "err", copyErr)
		return fmt.Errorf("write %s: %w", destinationPath, copyErr)
	}
	if closeErr != nil {
		slog.Warn("update copy close failed", "path", destinationPath, "err", closeErr)
		return fmt.Errorf("close %s: %w", destinationPath, closeErr)
	}
	return nil
}
