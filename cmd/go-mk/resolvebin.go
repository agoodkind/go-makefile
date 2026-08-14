package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"goodkind.io/go-makefile/selfupdate"
)

type resolveBinCommand string

const (
	resolveBinCommandBin         resolveBinCommand = "bin"
	resolveBinCommandSelectedBin resolveBinCommand = "selected-bin"
)

func runResolveBin() int {
	commandName := resolveBinCommandBin
	if len(os.Args) > 2 {
		commandName = resolveBinCommand(os.Args[2])
	}
	switch commandName {
	case resolveBinCommandBin:
		if err := resolveProvisionBinary(); err != nil {
			writeStderr("go-mk: " + err.Error() + "\n")
			return 1
		}
		return 0
	case resolveBinCommandSelectedBin:
		path, err := selectedProvisionBinaryPath()
		if err != nil {
			writeStderr("go-mk: " + err.Error() + "\n")
			return 1
		}
		if path != "" {
			writeStdout(path + "\n")
		}
		return 0
	default:
		writeStderr(fmt.Sprintf("go-mk resolve-bin: unknown command %s\n", commandName))
		return 2
	}
}

func resolveProvisionBinary() error {
	if path := strings.TrimSpace(os.Getenv("GO_MK_BIN")); path != "" {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("%s not executable", path)
		}
		return nil
	}
	outputPath := provisionBinaryOutputPath()
	buildRepo := strings.TrimSpace(os.Getenv("GO_MK_BUILD_REPO"))
	if buildRepo != "" {
		if err := buildDevBinary(buildRepo, outputPath); err != nil {
			return err
		}
		return nil
	}
	_, err := installRollingBinary(outputPath)
	return err
}

func selectedProvisionBinaryPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("GO_MK_BIN")); path != "" {
		return path, nil
	}
	outputPath := provisionBinaryOutputPath()
	if _, err := os.Stat(outputPath); err == nil {
		return outputPath, nil
	}
	return "", nil
}

func provisionBinaryOutputPath() string {
	root := strings.TrimSpace(os.Getenv("GO_MK_ROOT"))
	if root == "" {
		return filepath.Join(".make", "go-mk")
	}
	return filepath.Join(root, ".make", "go-mk")
}

func buildDevBinary(repoPath string, outputPath string) error {
	slog.Info("resolve-bin build dev binary", slog.String("repo", repoPath), slog.String("output", outputPath))
	if _, err := os.Stat(repoPath); err != nil {
		slog.Error("resolve-bin build repo missing", slog.String("repo", repoPath), slog.Any("err", err))
		return fmt.Errorf("build repo %s not present; skipping", repoPath)
	}
	packagePath := strings.TrimSpace(os.Getenv("GO_MK_BUILD_PKG"))
	if packagePath == "" {
		packagePath = "./cmd/go-mk"
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", outputPath, packagePath)
	cmd.Dir = repoPath
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("resolve-bin dev build failed", slog.Any("err", err))
		return fmt.Errorf("dev build failed: %w\n%s", err, output)
	}
	return nil
}

func installRollingBinary(outputPath string) (string, error) {
	slog.Info("resolve-bin install rolling binary", slog.String("output", outputPath))
	releaseBase := strings.TrimSpace(os.Getenv("GO_MK_RELEASE_BASE"))
	if releaseBase == "" {
		releaseBase = defaultReleaseDownload
	}
	repo := strings.TrimSpace(os.Getenv("GO_MK_API_REPO"))
	if repo == "" {
		repo = defaultProvisionAPIRepo
	}
	cacheDir := filepath.Join(".make", "go-mk-release-cache")
	result, err := selfupdate.InstallReleaseBinary(context.Background(), selfupdate.InstallReleaseBinaryOptions{
		Options: selfupdate.Options{
			Config: selfupdate.Config{
				Repo:       repo,
				Binary:     "go-mk",
				APIBaseURL: releaseAPIFromDownloadBase(releaseBase),
			},
			CacheDir: cacheDir,
		},
		Channel: selfupdate.ReleaseChannelRolling,
		BinDir:  filepath.Dir(outputPath),
	})
	if err != nil {
		return "", err
	}
	installedPath := result.InstallPath
	if installedPath != outputPath {
		if err := os.Rename(installedPath, outputPath); err != nil {
			return "", err
		}
	}
	return outputPath, nil
}

func releaseAPIFromDownloadBase(releaseBase string) string {
	releaseBase = strings.TrimRight(strings.TrimSpace(releaseBase), "/")
	if strings.HasSuffix(releaseBase, "/api.github.com") {
		return releaseBase
	}
	if strings.Contains(releaseBase, "github.com") {
		return "https://api.github.com"
	}
	return releaseBase
}
