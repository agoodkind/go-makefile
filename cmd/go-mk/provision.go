package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	provisionMakeDir       = ".make"
	provisionSelfAsset     = "scripts/go-mk-bootstrap.sh"
	provisionStatePath     = ".make/.go-mk-fetch-state"
	provisionReuseWindow   = time.Hour
	provisionLockWait      = 30 * time.Second
	defaultCodeloadBase    = "https://codeload.github.com"
	defaultProvisionRepo   = "agoodkind/go-makefile"
	defaultProvisionRef    = "main"
	fetchMaxTimeSeconds    = "15"
	fetchSpeedLimit        = "1024"
	fetchSpeedTime         = "3"
	fetchRetryMaxTime      = "4"
	validationConnect      = "2"
	validationMaxTime      = "3"
	fetchConnectTimeout    = "5"
)

type provisionConfig struct {
	apiRepo      string
	apiRef       string
	devDir       string
	modules      []string
	provisioned  bool
	codeloadBase string
}

func runProvision() int {
	cfg := loadProvisionConfig()
	if err := provisionAssets(cfg); err != nil {
		writeStderr(err.Error() + "\n")
		return 1
	}
	return 0
}

func loadProvisionConfig() provisionConfig {
	modules := strings.Fields(strings.TrimSpace(os.Getenv("GO_MK_MODULES")))
	codeloadBase := strings.TrimSpace(os.Getenv("GO_MK_CODELOAD_BASE"))
	if codeloadBase == "" {
		codeloadBase = defaultCodeloadBase
	}
	apiRepo := strings.TrimSpace(os.Getenv("GO_MK_API_REPO"))
	if apiRepo == "" {
		apiRepo = defaultProvisionRepo
	}
	apiRef := strings.TrimSpace(os.Getenv("GO_MK_API_REF"))
	if apiRef == "" {
		apiRef = defaultProvisionRef
	}
	return provisionConfig{
		apiRepo:      apiRepo,
		apiRef:       apiRef,
		devDir:       strings.TrimSpace(os.Getenv("GO_MK_DEV_DIR")),
		modules:      modules,
		provisioned:  strings.TrimSpace(os.Getenv("_GO_MK_PROVISIONED")) == "1",
		codeloadBase: strings.TrimRight(codeloadBase, "/"),
	}
}

func provisionAssets(cfg provisionConfig) error {
	if err := os.MkdirAll(provisionMakeDir, 0o755); err != nil {
		slog.Error("provision mkdir .make failed", slog.Any("err", err))
		return fmt.Errorf("go-mk provision: mkdir .make: %w", err)
	}
	release, err := acquireProvisionLock()
	if err != nil {
		return err
	}
	defer release()

	if cfg.devDir != "" {
		if err := installProvisionFromDevDir(cfg); err != nil {
			return fmt.Errorf("error: could not install from GO_MK_DEV_DIR=%s", cfg.devDir)
		}
		if err := provisionAssetsComplete(cfg, provisionMakeDir); err != nil {
			return fmt.Errorf("error: GO_MK_DEV_DIR=%s does not provide every required asset", cfg.devDir)
		}
		return nil
	}
	if cfg.provisioned {
		if err := provisionAssetsComplete(cfg, provisionMakeDir); err == nil {
			return nil
		}
		return fmt.Errorf("error: _GO_MK_PROVISIONED=1 but .make is missing a required asset")
	}

	knownETag := ""
	if !runningInCI() && provisionAssetsComplete(cfg, provisionMakeDir) == nil {
		etag, knownRef := readProvisionState()
		if knownRef == cfg.apiRef {
			knownETag = etag
		}
	}
	if knownETag != "" {
		statusCode, probeErr := validateProvisionUpstream(cfg, knownETag)
		if statusCode == 304 {
			return nil
		}
		if !runningInCI() && probeErr != nil && provisionStateRecent() && provisionAssetsComplete(cfg, provisionMakeDir) == nil {
			serveProvisionFromDiskWarning(cfg)
			return nil
		}
		if probeErr != nil {
			writeStderr(fmt.Sprintf("validate_upstream: curl exited, falling back to a full fetch: %v\n", probeErr))
		}
	}
	if err := downloadAndInstallProvision(cfg); err != nil {
		return fmt.Errorf("error: could not provision go-makefile assets. Set GO_MK_DEV_DIR, or check network access to %s. If this helper itself is bad, delete %s/%s to force a fresh copy on the next run", cfg.codeloadBase, provisionMakeDir, provisionSelfAsset)
	}
	return nil
}

func provisionCodeloadURL(cfg provisionConfig) string {
	return cfg.codeloadBase + "/" + cfg.apiRepo + "/tar.gz/" + cfg.apiRef
}

func provisionRequiredAssets(cfg provisionConfig) []string {
	assets := []string{
		"go.mk",
		"golangci.yml",
		"notices.txt",
		"scripts/go-mk-fetch-one.sh",
		"scripts/go-mk-bin.sh",
		"scripts/go-mk-sync.sh",
		provisionSelfAsset,
	}
	return append(assets, cfg.modules...)
}

func provisionAssetsComplete(cfg provisionConfig, baseDir string) error {
	for _, assetName := range provisionRequiredAssets(cfg) {
		assetPath := filepath.Join(baseDir, assetName)
		info, err := os.Lstat(assetPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			writeStderr(fmt.Sprintf("error: %s is a symlink; refusing to treat it as a provisioned asset\n", assetPath))
			return fmt.Errorf("%s is a symlink", assetPath)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("%s is missing or empty", assetPath)
		}
	}
	return nil
}

func installProvisionFromDevDir(cfg provisionConfig) error {
	if err := clearProvisionState(); err != nil {
		return err
	}
	for _, assetName := range provisionRequiredAssets(cfg) {
		sourcePath := filepath.Join(cfg.devDir, assetName)
		if _, err := os.Stat(sourcePath); err != nil {
			continue
		}
		targetPath := filepath.Join(provisionMakeDir, assetName)
		if err := installProvisionAsset(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func installProvisionFromStage(stageDir string, cfg provisionConfig) error {
	for _, assetName := range provisionRequiredAssets(cfg) {
		sourcePath := filepath.Join(stageDir, assetName)
		targetPath := filepath.Join(provisionMakeDir, assetName)
		if err := installProvisionAsset(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func installProvisionAsset(sourcePath string, targetPath string) error {
	slog.Info("provision install asset", slog.String("source", sourcePath), slog.String("target", targetPath))
	if targetIsDirectory(targetPath) {
		return fmt.Errorf("error: %s is a directory (or a symlink to one); refusing to install an asset over it", targetPath)
	}
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("error: could not create %s", targetDir)
	}
	tempFile, err := os.CreateTemp(targetDir, filepath.Base(targetPath)+".tmp.")
	if err != nil {
		return fmt.Errorf("error: could not create a temporary file beside %s", targetPath)
	}
	tempName := tempFile.Name()
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempName)
		return fmt.Errorf("error: could not install %s into %s", sourcePath, targetPath)
	}
	_, copyErr := io.Copy(tempFile, sourceFile)
	_ = sourceFile.Close()
	closeErr := tempFile.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("error: could not install %s into %s", sourcePath, targetPath)
	}
	if strings.HasSuffix(targetPath, ".sh") {
		if err := os.Chmod(tempName, 0o755); err != nil {
			_ = os.Remove(tempName)
			return fmt.Errorf("error: could not mark %s executable", targetPath)
		}
	}
	if err := os.Rename(tempName, targetPath); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("error: could not move %s into place", targetPath)
	}
	return nil
}

func targetIsDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func validateProvisionUpstream(cfg provisionConfig, knownETag string) (int, error) {
	args := []string{
		"-sS", "--head",
		"--connect-timeout", validationConnect,
		"--max-time", validationMaxTime,
		"-o", os.DevNull,
		"-w", "%{http_code}",
	}
	if knownETag != "" {
		args = append(args, "-H", "If-None-Match: "+knownETag)
	}
	args = append(args, provisionCodeloadURL(cfg))
	stdout, stderr, exitCode, err := runCurl(args)
	if err != nil {
		return 0, err
	}
	if exitCode != 0 {
		return 0, fmt.Errorf("curl exited %d: %s", exitCode, strings.TrimSpace(stderr))
	}
	status, convErr := strconv.Atoi(strings.TrimSpace(stdout))
	if convErr != nil {
		return 0, convErr
	}
	return status, nil
}

func downloadAndInstallProvision(cfg provisionConfig) error {
	slog.Info("provision download archive", slog.String("url", provisionCodeloadURL(cfg)))
	stageRoot, err := os.MkdirTemp(os.TempDir(), "go-mk-stage.")
	if err != nil {
		return fmt.Errorf("error: could not create a staging directory (TMPDIR=%s): a local setup problem, not necessarily a network one", os.TempDir())
	}
	defer func() { _ = os.RemoveAll(stageRoot) }()

	headersPath := filepath.Join(stageRoot, "headers")
	archivePath := filepath.Join(stageRoot, "snapshot.tar.gz")
	status, err := curlGetArchive(provisionCodeloadURL(cfg), archivePath, headersPath)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("error: tarball fetch returned HTTP %d from %s", status, cfg.codeloadBase)
	}
	stageDir := filepath.Join(stageRoot, "tree")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return err
	}
	tarCmd := exec.Command("tar", "-xzf", archivePath, "-C", stageDir, "--strip-components", "1")
	if output, tarErr := tarCmd.CombinedOutput(); tarErr != nil {
		return fmt.Errorf("error: tar extract exited: %s", strings.TrimSpace(string(output)))
	}
	if err := provisionAssetsComplete(cfg, stageDir); err != nil {
		return fmt.Errorf("error: staged tree is missing a required asset")
	}
	if err := clearProvisionState(); err != nil {
		return err
	}
	if err := installProvisionFromStage(stageDir, cfg); err != nil {
		return err
	}
	if err := provisionAssetsComplete(cfg, provisionMakeDir); err != nil {
		return fmt.Errorf("error: .make is incomplete after install")
	}
	etagValue := etagFromHeaders(headersPath)
	if etagValue == "" {
		_ = os.Remove(provisionStatePath)
		writeStderr(fmt.Sprintf("go-makefile: upstream served no ETag header; installed the fetched tree but could not record validation state, so every run will download unconditionally until ETag returns. Check upstream (%s).\n", cfg.codeloadBase))
		return nil
	}
	if err := writeProvisionState(cfg.apiRef, etagValue); err != nil {
		_ = os.Remove(provisionStatePath)
		writeStderr(fmt.Sprintf("go-makefile: installed the fetched tree but could not write %s, so every run will download unconditionally until that path is writable\n", provisionStatePath))
	}
	return nil
}

func curlGetArchive(url string, archivePath string, headersPath string) (int, error) {
	slog.Info("provision curl get archive", slog.String("url", url))
	args := []string{
		"-sS",
		"--connect-timeout", fetchConnectTimeout,
		"--max-time", fetchMaxTimeSeconds,
		"--speed-limit", fetchSpeedLimit,
		"--speed-time", fetchSpeedTime,
		"--retry", "3",
		"--retry-delay", "2",
		"--retry-max-time", fetchRetryMaxTime,
		"-D", headersPath,
		"-o", archivePath,
		"-w", "%{http_code}",
		url,
	}
	stdout, stderr, exitCode, err := runCurl(args)
	if err != nil {
		return 0, err
	}
	if exitCode != 0 {
		return 0, fmt.Errorf("error: curl exited %d fetching the tarball: %s", exitCode, strings.TrimSpace(stderr))
	}
	status, convErr := strconv.Atoi(strings.TrimSpace(stdout))
	if convErr != nil {
		return 0, convErr
	}
	return status, nil
}

func runCurl(args []string) (string, string, int, error) {
	slog.Debug("provision curl", slog.Int("args", len(args)))
	command := exec.Command("curl", args...)
	var stdout strings.Builder
	var stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0, nil
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return stdout.String(), stderr.String(), 0, err
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
}

func etagFromHeaders(path string) string {
	slog.Debug("provision read etag headers", slog.String("path", path))
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	value := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), "etag") {
			value = strings.TrimSpace(parts[1])
		}
	}
	return value
}

func readProvisionState() (string, string) {
	slog.Debug("provision read state", slog.String("path", provisionStatePath))
	content, err := os.ReadFile(provisionStatePath)
	if err != nil {
		return "", ""
	}
	etag := ""
	ref := ""
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "etag=") {
			etag = strings.TrimPrefix(line, "etag=")
		}
		if strings.HasPrefix(line, "ref=") {
			ref = strings.TrimPrefix(line, "ref=")
		}
	}
	return etag, ref
}

func writeProvisionState(ref string, etag string) error {
	slog.Info("provision write state", slog.String("ref", ref))
	content := fmt.Sprintf("ref=%s\netag=%s\ntimestamp=%d\n", ref, etag, time.Now().Unix())
	return os.WriteFile(provisionStatePath, []byte(content), 0o644)
}

func clearProvisionState() error {
	slog.Debug("provision clear state", slog.String("path", provisionStatePath))
	removalError := ""
	if err := os.Remove(provisionStatePath); err != nil && !os.IsNotExist(err) {
		removalError = err.Error()
	}
	if _, err := os.Lstat(provisionStatePath); err == nil {
		return fmt.Errorf("error: could not remove %s, so refusing to modify .make while stale validation state survives: %s", provisionStatePath, removalError)
	}
	return nil
}

func provisionStateRecent() bool {
	content, err := os.ReadFile(provisionStatePath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "timestamp=") {
			continue
		}
		recorded, err := strconv.ParseInt(strings.TrimPrefix(line, "timestamp="), 10, 64)
		if err != nil {
			return false
		}
		now := time.Now().Unix()
		if recorded > now {
			return false
		}
		return now-recorded <= int64(provisionReuseWindow.Seconds())
	}
	return false
}

func serveProvisionFromDiskWarning(cfg provisionConfig) {
	etag, _ := readProvisionState()
	if etag == "" {
		etag = "unknown"
	}
	ageDisplay := "an unknown time"
	content, err := os.ReadFile(provisionStatePath)
	if err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			if !strings.HasPrefix(line, "timestamp=") {
				continue
			}
			recorded, convErr := strconv.ParseInt(strings.TrimPrefix(line, "timestamp="), 10, 64)
			if convErr != nil {
				break
			}
			age := time.Now().Unix() - recorded
			if age < 60 {
				ageDisplay = fmt.Sprintf("%ds", age)
			} else {
				ageDisplay = fmt.Sprintf("%dm", age/60)
			}
		}
	}
	writeStderr(fmt.Sprintf("go-makefile: upstream unreachable; serving .make assets validated %s ago (etag %s). Set _GO_MK_PROVISIONED=1 to silence, or check network access to %s\n", ageDisplay, etag, cfg.codeloadBase))
}

func runningInCI() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" && strings.TrimSpace(os.Getenv("GITHUB_RUN_ID")) != ""
}

func acquireProvisionLock() (func(), error) {
	lockDir, err := provisionLockDir()
	if err != nil {
		return func() {}, err
	}
	deadline := time.Now().Add(provisionLockWait)
	for {
		mkdirErr := os.Mkdir(lockDir, 0o755)
		if mkdirErr == nil {
			pidPath := filepath.Join(lockDir, "pid")
			if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
				_ = os.RemoveAll(lockDir)
				return func() {}, fmt.Errorf("error: could not record the lock holder in %s: a local setup problem, not a lock conflict", lockDir)
			}
			return func() { _ = os.RemoveAll(lockDir) }, nil
		}
		if _, statErr := os.Stat(lockDir); statErr != nil {
			slog.Error("provision lock mkdir failed", slog.String("dir", lockDir), slog.Any("err", mkdirErr))
			return func() {}, fmt.Errorf("error: could not create the lock directory %s: %v. This is a local setup problem, not another build holding the lock.", lockDir, mkdirErr)
		}
		if staleProvisionLock(lockDir) {
			staleName := lockDir + ".stale." + strconv.Itoa(os.Getpid())
			if err := os.Rename(lockDir, staleName); err == nil {
				_ = os.RemoveAll(staleName)
				continue
			}
		}
		if time.Now().After(deadline) {
			return func() {}, fmt.Errorf("error: another go-makefile parse has held %s for %ds. If no other build is running, remove that directory.", lockDir, int(provisionLockWait.Seconds()))
		}
		time.Sleep(time.Second)
	}
}

func provisionLockDir() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	physical, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		physical = workingDir
	}
	sum := sha1.Sum([]byte(physical))
	digest := hex.EncodeToString(sum[:])
	return filepath.Join(os.TempDir(), "go-mk-lock-"+digest), nil
}

func staleProvisionLock(lockDir string) bool {
	slog.Debug("provision check stale lock", slog.String("dir", lockDir))
	body, err := os.ReadFile(filepath.Join(lockDir, "pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil || pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return true
	}
	return false
}
