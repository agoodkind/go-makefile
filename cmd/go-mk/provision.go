package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	provisionMakeDir         = ".make"
	provisionSelfAsset       = "scripts/go-mk-bootstrap.sh"
	provisionStatePath       = ".make/.go-mk-fetch-state"
	provisionReuseWindow     = time.Hour
	provisionFetchMaxTime    = 15 * time.Second
	provisionValidationMax   = 3 * time.Second
	provisionHeaderTimeout   = 3 * time.Second
	defaultReleaseDownload   = "https://github.com"
	defaultCodeloadBase      = "https://codeload.github.com"
	defaultProvisionAPIRepo  = "agoodkind/go-makefile"
	defaultProvisionAPIRef   = "main"
)

type provisionConfig struct {
	apiRepo      string
	apiRef       string
	devDir       string
	modules      []string
	provisioned  bool
	releaseBase  string
	codeloadBase string
	skipFetch    bool
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
	modulesText := strings.TrimSpace(os.Getenv("GO_MK_MODULES"))
	var modules []string
	for _, moduleName := range strings.Fields(modulesText) {
		modules = append(modules, moduleName)
	}
	releaseBase := strings.TrimSpace(os.Getenv("GO_MK_RELEASE_BASE"))
	if releaseBase == "" {
		releaseBase = defaultReleaseDownload
	}
	codeloadBase := strings.TrimSpace(os.Getenv("GO_MK_CODELOAD_BASE"))
	if codeloadBase == "" {
		codeloadBase = defaultCodeloadBase
	}
	apiRepo := strings.TrimSpace(os.Getenv("GO_MK_API_REPO"))
	if apiRepo == "" {
		apiRepo = defaultProvisionAPIRepo
	}
	apiRef := strings.TrimSpace(os.Getenv("GO_MK_API_REF"))
	if apiRef == "" {
		apiRef = defaultProvisionAPIRef
	}
	return provisionConfig{
		apiRepo:      apiRepo,
		apiRef:       apiRef,
		devDir:       strings.TrimSpace(os.Getenv("GO_MK_DEV_DIR")),
		modules:      modules,
		provisioned:  strings.TrimSpace(os.Getenv("_GO_MK_PROVISIONED")) == "1",
		releaseBase:  strings.TrimRight(releaseBase, "/"),
		codeloadBase: strings.TrimRight(codeloadBase, "/"),
		skipFetch:    envTruthy(os.Getenv("GO_MK_SKIP_FETCH")),
	}
}

func provisionAssets(cfg provisionConfig) error {
	slog.Info("provision assets")
	if err := os.MkdirAll(provisionMakeDir, 0o755); err != nil {
		slog.Error("provision mkdir .make failed", slog.Any("err", err))
		return fmt.Errorf("go-mk provision: mkdir .make: %w", err)
	}
	if err := checkProvisionLocalSetup(); err != nil {
		return err
	}
	release, err := acquireProvisionLock()
	if err != nil {
		return err
	}
	defer release()

	if cfg.devDir != "" {
		if err := installProvisionFromDevDir(cfg); err != nil {
			slog.Error("provision install from GO_MK_DEV_DIR failed", slog.String("dir", cfg.devDir), slog.Any("err", err))
			return fmt.Errorf("go-mk provision: install from GO_MK_DEV_DIR=%s: %w", cfg.devDir, err)
		}
		if !provisionAssetsComplete(cfg, provisionMakeDir) {
			return fmt.Errorf("go-mk provision: GO_MK_DEV_DIR=%s does not provide every required asset", cfg.devDir)
		}
		return nil
	}
	if cfg.provisioned {
		if provisionAssetsComplete(cfg, provisionMakeDir) {
			return nil
		}
		return fmt.Errorf("go-mk provision: _GO_MK_PROVISIONED=1 but .make is missing a required asset")
	}
	if cfg.skipFetch && provisionAssetsComplete(cfg, provisionMakeDir) {
		return nil
	}

	knownETag := ""
	knownRef := ""
	if !runningInCI() && provisionAssetsComplete(cfg, provisionMakeDir) {
		knownETag, knownRef = readProvisionState()
		if knownRef != cfg.apiRef {
			knownETag = ""
		}
	}
	if knownETag != "" {
		statusCode, probeErr := validateProvisionUpstream(cfg, knownETag)
		if statusCode == http.StatusNotModified {
			return nil
		}
		if !runningInCI() && provisionStateRecent() && provisionAssetsComplete(cfg, provisionMakeDir) && probeErr != nil {
			serveProvisionFromDiskWarning(cfg)
			return nil
		}
		if probeErr != nil {
			writeStderr(fmt.Sprintf("go-makefile: validate_upstream: %v\n", probeErr))
		}
	}
	if err := downloadAndInstallProvision(cfg); err != nil {
		return fmt.Errorf("go-mk provision: could not provision go-makefile assets. Set GO_MK_DEV_DIR, or check network access to %s", cfg.releaseBase)
	}
	return nil
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
	assets = append(assets, cfg.modules...)
	return assets
}

func provisionAssetsComplete(cfg provisionConfig, baseDir string) bool {
	for _, assetName := range provisionRequiredAssets(cfg) {
		assetPath := filepath.Join(baseDir, assetName)
		info, err := os.Lstat(assetPath)
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return false
		}
	}
	return true
}

func installProvisionFromDevDir(cfg provisionConfig) error {
	slog.Info("provision install from GO_MK_DEV_DIR", slog.String("dir", cfg.devDir))
	if err := clearProvisionState(); err != nil {
		return err
	}
	for _, assetName := range provisionRequiredAssets(cfg) {
		sourcePath := filepath.Join(cfg.devDir, assetName)
		if _, err := os.Stat(sourcePath); err != nil {
			continue
		}
		targetPath := filepath.Join(provisionMakeDir, assetName)
		if assetName == provisionSelfAsset {
			if err := installProvisionSelf(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		if err := installProvisionAsset(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func installProvisionAsset(sourcePath string, targetPath string) error {
	slog.Info("provision install asset", slog.String("source", sourcePath), slog.String("target", targetPath))
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if resolved, statErr := os.Stat(targetPath); statErr == nil && resolved.IsDir() {
				return fmt.Errorf("go-mk provision: refusing to install over symlink %s that points to a directory", targetPath)
			}
		}
	}
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	tempPath, err := os.CreateTemp(targetDir, filepath.Base(targetPath)+".tmp.")
	if err != nil {
		return err
	}
	tempName := tempPath.Name()
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		_ = tempPath.Close()
		_ = os.Remove(tempName)
		return err
	}
	if _, err := io.Copy(tempPath, sourceFile); err != nil {
		_ = sourceFile.Close()
		_ = tempPath.Close()
		_ = os.Remove(tempName)
		return err
	}
	_ = sourceFile.Close()
	if err := tempPath.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if strings.HasSuffix(targetPath, ".sh") {
		if err := os.Chmod(tempName, 0o755); err != nil {
			_ = os.Remove(tempName)
			return err
		}
	}
	return os.Rename(tempName, targetPath)
}

func installProvisionSelf(sourcePath string, targetPath string) error {
	if err := installProvisionAsset(sourcePath, targetPath); err != nil {
		return err
	}
	return os.Chmod(targetPath, 0o755)
}

func resolveProvisionReleaseTag(apiRef string) string {
	ref := strings.TrimSpace(apiRef)
	if ref == "" || ref == "main" || ref == "rolling" {
		return rollingReleaseTagName
	}
	return ref
}

func provisionSourceURL(cfg provisionConfig) string {
	tag := resolveProvisionReleaseTag(cfg.apiRef)
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", cfg.releaseBase, cfg.apiRepo, tag, sourceArchiveName)
}

func provisionCodeloadURL(cfg provisionConfig) string {
	return fmt.Sprintf("%s/%s/tar.gz/%s", cfg.codeloadBase, cfg.apiRepo, cfg.apiRef)
}

func validateProvisionUpstream(cfg provisionConfig, knownETag string) (int, error) {
	slog.Info("provision validate upstream", slog.String("url", provisionSourceURL(cfg)))
	request, err := http.NewRequest(http.MethodHead, provisionSourceURL(cfg), nil)
	if err != nil {
		return 0, err
	}
	if knownETag != "" {
		request.Header.Set("If-None-Match", knownETag)
	}
	client := provisionHTTPClient(provisionValidationMax)
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode, nil
}

func downloadAndInstallProvision(cfg provisionConfig) error {
	slog.Info("provision download and install", slog.String("url", provisionSourceURL(cfg)))
	sourceURL := provisionSourceURL(cfg)
	body, etag, statusCode, err := downloadProvisionArchive(sourceURL)
	if statusCode == http.StatusNotFound && resolveProvisionReleaseTag(cfg.apiRef) != rollingReleaseTagName {
		slog.Info("provision release asset missing; falling back to codeload", slog.String("ref", cfg.apiRef))
		body, etag, statusCode, err = downloadProvisionCodeload(cfg)
		sourceURL = provisionCodeloadURL(cfg)
	}
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK {
		return fmt.Errorf("archive fetch returned HTTP %d from %s", statusCode, sourceURL)
	}
	stageDir, cleanup, err := extractProvisionArchive(body)
	if err != nil {
		return err
	}
	defer cleanup()
	if !provisionAssetsComplete(cfg, stageDir) {
		return fmt.Errorf("staged tree is missing a required asset")
	}
	if err := clearProvisionState(); err != nil {
		return err
	}
	if err := installProvisionFromStage(stageDir, cfg); err != nil {
		return err
	}
	if !provisionAssetsComplete(cfg, provisionMakeDir) {
		return fmt.Errorf(".make is incomplete after install")
	}
	if etag == "" {
		_ = clearProvisionState()
		writeStderr(fmt.Sprintf("go-makefile: upstream served no ETag header; installed the fetched tree but could not record validation state, so every run will download unconditionally until ETag returns. Check upstream (%s).\n", cfg.releaseBase))
		return nil
	}
	if err := writeProvisionState(cfg.apiRef, etag); err != nil {
		_ = clearProvisionState()
		writeStderr(fmt.Sprintf("go-makefile: installed the fetched tree but could not write %s, so every run will download unconditionally until that path is writable\n", provisionStatePath))
	}
	return nil
}

func downloadProvisionArchive(url string) ([]byte, string, int, error) {
	slog.Info("provision download archive", slog.String("url", url))
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", 0, err
	}
	client := provisionHTTPClient(provisionFetchMaxTime)
	response, err := client.Do(request)
	if err != nil {
		return nil, "", 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return nil, "", response.StatusCode, fmt.Errorf("archive not found at %s", url)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, "", response.StatusCode, err
	}
	etag := strings.TrimSpace(response.Header.Get("ETag"))
	return body, etag, response.StatusCode, nil
}

func downloadProvisionCodeload(cfg provisionConfig) ([]byte, string, int, error) {
	url := provisionCodeloadURL(cfg)
	slog.Info("provision codeload fallback", slog.String("url", url))
	return downloadProvisionArchive(url)
}

func extractProvisionArchive(body []byte) (string, func(), error) {
	slog.Info("provision extract archive", slog.Int("bytes", len(body)))
	stageRoot, err := os.MkdirTemp("", "go-mk-stage.")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(stageRoot) }
	archivePath := filepath.Join(stageRoot, "snapshot.tar.gz")
	if err := os.WriteFile(archivePath, body, 0o644); err != nil {
		cleanup()
		return "", func() {}, err
	}
	stageDir := filepath.Join(stageRoot, "tree")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := extractSourceArchiveWithSystemTar(archivePath, stageDir); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return stageDir, cleanup, nil
}

func installProvisionFromStage(stageDir string, cfg provisionConfig) error {
	slog.Info("provision install from stage", slog.String("dir", stageDir))
	for _, assetName := range provisionRequiredAssets(cfg) {
		sourcePath := filepath.Join(stageDir, assetName)
		targetPath := filepath.Join(provisionMakeDir, assetName)
		if assetName == provisionSelfAsset {
			if err := installProvisionSelf(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		if err := installProvisionAsset(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func readProvisionState() (string, string) {
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
	slog.Info("provision write state", slog.String("path", provisionStatePath), slog.String("ref", ref))
	content := fmt.Sprintf("ref=%s\netag=%s\ntimestamp=%d\n", ref, etag, time.Now().Unix())
	return os.WriteFile(provisionStatePath, []byte(content), 0o644)
}

func clearProvisionState() error {
	slog.Info("provision clear state", slog.String("path", provisionStatePath))
	if err := os.Remove(provisionStatePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(provisionStatePath); err == nil {
		return fmt.Errorf("could not remove %s", provisionStatePath)
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
		age := time.Since(time.Unix(recorded, 0))
		return age >= 0 && age <= provisionReuseWindow
	}
	return false
}

func serveProvisionFromDiskWarning(cfg provisionConfig) {
	etag, _ := readProvisionState()
	writeStderr(fmt.Sprintf("go-makefile: upstream unreachable; serving .make assets validated within the reuse window (etag %s). Set _GO_MK_PROVISIONED=1 to silence, or check network access to %s\n", etag, cfg.releaseBase))
}

func runningInCI() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" && strings.TrimSpace(os.Getenv("GITHUB_RUN_ID")) != ""
}

func provisionHTTPClient(totalTimeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: totalTimeout,
		Transport: &http.Transport{
			ResponseHeaderTimeout: provisionHeaderTimeout,
		},
	}
}

func checkProvisionLocalSetup() error {
	tmpDir := strings.TrimSpace(os.Getenv("TMPDIR"))
	if tmpDir == "" {
		return nil
	}
	probe, err := os.CreateTemp(tmpDir, "go-mk-probe.")
	if err != nil {
		slog.Error("provision TMPDIR is not writable", slog.String("tmpdir", tmpDir), slog.Any("err", err))
		return fmt.Errorf(
			"go-mk provision: local setup problem: TMPDIR %s is not writable: %w (this is not a lock conflict)",
			tmpDir,
			err,
		)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return nil
}

func acquireProvisionLock() (func(), error) {
	slog.Info("provision acquire lock")
	consumerPath, err := os.Getwd()
	if err != nil {
		return func() {}, err
	}
	digest := sha256.Sum256([]byte(consumerPath))
	lockDir := filepath.Join(os.TempDir(), "go-mk-lock-"+hex.EncodeToString(digest[:8]))
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := os.Mkdir(lockDir, 0o755); err == nil {
			return func() { _ = os.RemoveAll(lockDir) }, nil
		}
		if time.Now().After(deadline) {
			return func() {}, fmt.Errorf("go-mk provision: timed out waiting for lock %s", lockDir)
		}
		time.Sleep(time.Second)
	}
}
