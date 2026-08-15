package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		if err := resolveEngineBinary(); err != nil {
			writeStderr("go-mk: " + err.Error() + "\n")
			return 1
		}
		return 0
	case resolveBinCommandSelectedBin:
		path, err := selectedEngineBinaryPath()
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

func resolveEngineBinary() error {
	if configured := strings.TrimSpace(os.Getenv("GO_MK_BIN")); configured != "" {
		if !executableFile(configured) {
			return fmt.Errorf("%s not executable", configured)
		}
		if missingRequiredFlags(configured) {
			return fmt.Errorf("%s does not support the required capabilities", configured)
		}
		return nil
	}
	outputPath := engineBinaryOutputPath()
	repoPath := strings.TrimSpace(os.Getenv("GO_MK_BUILD_REPO"))
	if repoPath != "" {
		if _, err := os.Stat(repoPath); err != nil {
			writeStderr(fmt.Sprintf("go-mk: build repo %s not present; skipping\n", repoPath))
			return nil
		}
		rebuildReason := ""
		if !executableFile(outputPath) {
			rebuildReason = "missing cached binary"
		} else if isSymlink(outputPath) {
			rebuildReason = "cached binary is an install symlink, not a dev build"
		} else if sourceNewerThanEngine(repoPath, outputPath) {
			rebuildReason = "source newer than cached binary"
		} else if missingRequiredFlags(outputPath) {
			rebuildReason = "cached binary missing required capabilities"
		}
		if rebuildReason != "" {
			return buildEngineFromRepo(repoPath, outputPath)
		}
		return nil
	}
	return installEngineBinary(outputPath)
}

func selectedEngineBinaryPath() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("GO_MK_BIN")); configured != "" {
		return configured, nil
	}
	outputPath := engineBinaryOutputPath()
	if executableFile(outputPath) {
		return outputPath, nil
	}
	return "", nil
}

func engineBinaryOutputPath() string {
	root := strings.TrimSpace(os.Getenv("_GO_MK_ROOT"))
	if root == "" {
		root = strings.TrimSpace(os.Getenv("GO_MK_ROOT"))
	}
	if root == "" {
		return filepath.Join(".make", "go-mk")
	}
	return filepath.Join(root, ".make", "go-mk")
}

func buildEngineFromRepo(repoPath string, outputPath string) error {
	packagePath := strings.TrimSpace(os.Getenv("GO_MK_BUILD_PKG"))
	if packagePath == "" {
		packagePath = "./cmd/go-mk"
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(outputPath)
	slog.Info("resolve-bin build from repo", slog.String("repo", repoPath), slog.String("output", outputPath))
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

func installEngineBinary(outputPath string) error {
	installSpec := strings.TrimSpace(os.Getenv("GO_MK_INSTALL"))
	if installSpec == "" {
		installSpec = "goodkind.io/go-makefile/cmd/go-mk@main"
	}
	slog.Info("resolve-bin install binary", slog.String("spec", installSpec))
	binaryName := filepath.Base(strings.SplitN(installSpec, "@", 2)[0])
	gopath, err := goEnvPath("GOPATH")
	if err != nil {
		return err
	}
	goBin := filepath.Join(gopath, "bin")
	installedPath := filepath.Join(goBin, binaryName)
	cmd := exec.Command("go", "install", installSpec)
	cmd.Env = append(os.Environ(),
		"GOPROXY=direct",
		"GOPRIVATE=goodkind.io/go-makefile",
		"GOBIN="+goBin,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		writeStderr(string(output))
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(outputPath)
	return os.Symlink(installedPath, outputPath)
}

func sourceNewerThanEngine(repoPath string, outputPath string) bool {
	info, err := os.Stat(outputPath)
	if err != nil {
		return true
	}
	binaryTime := info.ModTime()
	roots := []string{
		filepath.Join(repoPath, "cmd", "go-mk"),
		filepath.Join(repoPath, "internal"),
		filepath.Join(repoPath, "go.mod"),
		filepath.Join(repoPath, "go.sum"),
	}
	newer := false
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, fileInfo os.FileInfo, walkErr error) error {
			if walkErr != nil || fileInfo == nil || fileInfo.IsDir() {
				return walkErr
			}
			name := fileInfo.Name()
			if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" {
				return nil
			}
			if fileInfo.ModTime().After(binaryTime) {
				newer = true
			}
			return nil
		})
		if newer {
			return true
		}
	}
	return false
}

func missingRequiredFlags(binaryPath string) bool {
	slog.Info("resolve-bin probe flags", slog.String("binary", binaryPath))
	cmd := exec.Command(binaryPath, "-flags")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return true
	}
	return !strings.Contains(string(output), "write-batch")
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}
