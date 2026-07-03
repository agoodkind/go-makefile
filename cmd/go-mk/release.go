// release orchestration for go-mk. It cross-compiles the binary for each
// configured platform with CGO disabled, signs and notarizes the darwin
// binaries with anchore/quill (invoked as a process), writes tar.gz archives
// and a sha256 checksums file with the standard library, pushes a release tag,
// and publishes a GitHub release with gh. It replaces the previous GoReleaser
// plus shell pipeline so the whole release flow lives in one Go command with no
// shell script. It owns process execution and file I/O; process boundaries emit
// a structured slog event and return the raw error for the caller to surface.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// defaultReleasePlatforms is the os/arch matrix built when RELEASE_PLATFORMS is
// unset. CGO is disabled, so darwin binaries cross-compile from any host.
const defaultReleasePlatforms = "darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"

const (
	defaultDarwinSignAttempts = 3
	defaultDarwinRetryDelay   = 10 * time.Second
)

// releaseConfig is the resolved release input, read once from the environment
// that the make layer exports (BINARY, CMD, VPKG, GKLOG_VPKG, RELEASE_*).
type releaseConfig struct {
	binary                string
	mainPkg               string
	binaries              []releaseBinary
	versionPkg            string
	gklogPkg              string
	entitlements          string
	requireDarwinCodesign bool
	platforms             []string
	distDir               string
	tag                   string
	shortSHA              string
	targetSHA             string
	buildTime             string
	prerelease            bool
}

type releaseBinary struct {
	name    string
	mainPkg string
}

var (
	releaseRunProcess       = runProcess
	releaseSleep            = time.Sleep
	darwinSignAttempts      = defaultDarwinSignAttempts
	darwinSignRetryInterval = defaultDarwinRetryDelay
)

// runRelease loads the release configuration and runs the requested stage. The
// matrix workflow drives three stages: tag computes one shared tag, build runs
// per native runner (one platform, cgo honoured, darwin signed), and publish
// collects every uploaded archive into the GitHub release. An unset stage keeps
// the single-runner all-in-one pipeline for a pure-Go consumer that does not
// need the matrix. It returns the process exit code.
func runRelease() int {
	cfg, err := loadReleaseConfig()
	if err != nil {
		return statusFromError(err)
	}
	switch releaseStage(strings.TrimSpace(os.Getenv("RELEASE_STAGE"))) {
	case stageTag:
		return statusFromError(emitReleaseTag(cfg))
	case stageBuild:
		return statusFromError(buildStage(cfg))
	case stagePublish:
		return statusFromError(publishStage(cfg))
	default:
		return statusFromError(executeRelease(cfg))
	}
}

// releaseStage is the named enum of release stages the matrix workflow drives
// through RELEASE_STAGE. An empty or unrecognized value runs the single-runner
// all-in-one pipeline.
type releaseStage string

const (
	stageTag     releaseStage = "tag"
	stageBuild   releaseStage = "build"
	stagePublish releaseStage = "publish"
)

// emitReleaseTag prints the computed tag so the matrix workflow threads one
// consistent tag to every build and publish job. It writes the tag to stdout
// and, under GitHub Actions, appends `tag=<tag>` to GITHUB_OUTPUT so the calling
// step exposes it as an output.
func emitReleaseTag(cfg releaseConfig) error {
	writeStdout(cfg.tag + "\n")
	outputPath := strings.TrimSpace(os.Getenv("GITHUB_OUTPUT"))
	if outputPath == "" {
		return nil
	}
	slog.Info("release emit tag", slog.String("tag", cfg.tag))
	file, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.WriteString("tag=" + cfg.tag + "\n")
	return err
}

// buildStage builds, signs, and archives the configured platforms without
// pushing a tag or publishing. Each native runner sets RELEASE_PLATFORMS to its
// own os/arch, so this builds one platform with the runner's toolchain; cgo is
// honoured through CGO_ENABLED and the system libraries the workflow installed.
func buildStage(cfg releaseConfig) error {
	if err := os.MkdirAll(cfg.distDir, 0o755); err != nil {
		return err
	}
	if err := checkCgoStub(); err != nil {
		return err
	}
	for _, platform := range cfg.platforms {
		if err := buildPlatform(cfg, platform); err != nil {
			return err
		}
	}
	if err := signDarwinBinaries(cfg); err != nil {
		return err
	}
	_, err := archivePlatforms(cfg)
	return err
}

// publishStage pushes the prerelease tag, writes checksums over the archives the
// build jobs uploaded into the dist directory, and publishes the GitHub release.
// It runs once after the build matrix completes.
func publishStage(cfg releaseConfig) error {
	if err := pushReleaseTag(cfg); err != nil {
		return err
	}
	archives, err := distArchives(cfg.distDir)
	if err != nil {
		return err
	}
	checksums, err := writeChecksums(cfg, archives)
	if err != nil {
		return err
	}
	return publishRelease(cfg, append(archives, checksums))
}

// distArchives returns the sorted tar.gz archives present in the dist directory,
// the per-platform artifacts the build jobs uploaded for this release.
func distArchives(distDir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(distDir, "*.tar.gz"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// loadReleaseConfig resolves the release inputs from the environment and git.
func loadReleaseConfig() (releaseConfig, error) {
	binary := strings.TrimSpace(os.Getenv("BINARY"))
	if binary == "" {
		return releaseConfig{}, fmt.Errorf("release: BINARY is not set")
	}
	mainPkg := strings.TrimSpace(os.Getenv("CMD"))
	if mainPkg == "" {
		mainPkg = "."
	}
	binaries, err := parseReleaseBinaries(binary, mainPkg, os.Getenv("RELEASE_BINS"))
	if err != nil {
		return releaseConfig{}, err
	}
	primaryBinary := binaries[0]
	platformsText := strings.TrimSpace(os.Getenv("RELEASE_PLATFORMS"))
	if platformsText == "" {
		platformsText = defaultReleasePlatforms
	}
	distDir := strings.TrimSpace(os.Getenv("DIST_DIR"))
	if distDir == "" {
		distDir = "dist"
	}

	shortSHA, err := gitOutput("rev-parse", "--short", "HEAD")
	if err != nil {
		return releaseConfig{}, err
	}
	targetSHA := strings.TrimSpace(os.Getenv("GITHUB_SHA"))
	if targetSHA == "" {
		targetSHA, err = gitOutput("rev-parse", "HEAD")
		if err != nil {
			return releaseConfig{}, err
		}
	}

	now := time.Now().UTC()
	runNumber := strings.TrimSpace(os.Getenv("GITHUB_RUN_NUMBER"))
	if runNumber == "" {
		runNumber = "0"
	}
	hexRun := runNumber
	if parsed, convErr := strconv.ParseInt(runNumber, 10, 64); convErr == nil {
		hexRun = strconv.FormatInt(parsed, 16)
	}

	// A push of a v-prefixed tag is a stable release that reuses that tag; any
	// other trigger (a main-branch commit, a manual dispatch) is a rolling
	// prerelease under a computed timestamp tag. GitHub renders the two
	// differently: stable carries the green "Latest" badge, prerelease the
	// "Pre-release" badge.
	refName := strings.TrimSpace(os.Getenv("GITHUB_REF_NAME"))
	stable := isStableRef(strings.TrimSpace(os.Getenv("GITHUB_REF")), refName)
	tag := fmt.Sprintf("%s-%s-%s", now.Format("200601021504"), hexRun, shortSHA)
	if stable {
		tag = refName
	}
	// The matrix tag job computes the timestamp tag once and threads it to every
	// build and publish job through RELEASE_TAG so each native runner agrees on
	// one tag; a stable v-tag is already identical across runners.
	if forced := strings.TrimSpace(os.Getenv("RELEASE_TAG")); forced != "" {
		tag = forced
	}

	return releaseConfig{
		binary:                primaryBinary.name,
		mainPkg:               primaryBinary.mainPkg,
		binaries:              binaries,
		versionPkg:            strings.TrimSpace(os.Getenv("VPKG")),
		gklogPkg:              strings.TrimSpace(os.Getenv("GKLOG_VPKG")),
		entitlements:          strings.TrimSpace(os.Getenv("RELEASE_ENTITLEMENTS")),
		requireDarwinCodesign: envTruthy(os.Getenv("REQUIRE_DARWIN_CODESIGN")),
		platforms:             strings.Fields(platformsText),
		distDir:               distDir,
		tag:                   tag,
		shortSHA:              shortSHA,
		targetSHA:             targetSHA,
		buildTime:             now.Format("2006-01-02T15:04:05Z"),
		prerelease:            !stable,
	}, nil
}

func parseReleaseBinaries(binary string, mainPkg string, releaseBinsText string) ([]releaseBinary, error) {
	fields := strings.Fields(releaseBinsText)
	if len(fields) == 0 {
		return []releaseBinary{{name: binary, mainPkg: mainPkg}}, nil
	}
	binaries := make([]releaseBinary, 0, len(fields))
	for _, field := range fields {
		parts := strings.Split(field, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("release: malformed RELEASE_BINS entry %q (want name:cmd)", field)
		}
		name := strings.TrimSpace(parts[0])
		mainPackage := strings.TrimSpace(parts[1])
		if name == "" || mainPackage == "" {
			return nil, fmt.Errorf("release: malformed RELEASE_BINS entry %q (want name:cmd)", field)
		}
		binaries = append(binaries, releaseBinary{name: name, mainPkg: mainPackage})
	}
	// RELEASE_BINS is the full set of binaries the release ships, not an
	// addition to BINARY. Require it to include the primary BINARY so an
	// incomplete list cannot silently drop the primary binary, and move the
	// primary to the front so entry order never changes which binary titles
	// the GitHub release.
	primaryIndex := -1
	for index, candidate := range binaries {
		if candidate.name == binary {
			primaryIndex = index
			break
		}
	}
	if primaryIndex < 0 {
		return nil, fmt.Errorf("release: RELEASE_BINS must include the primary binary %q", binary)
	}
	if primaryIndex > 0 {
		primary := binaries[primaryIndex]
		binaries = append(binaries[:primaryIndex], binaries[primaryIndex+1:]...)
		binaries = append([]releaseBinary{primary}, binaries...)
	}
	return binaries, nil
}

func envTruthy(value string) bool {
	truthyValues := map[string]struct{}{
		"1":    {},
		"true": {},
		"yes":  {},
		"on":   {},
	}
	_, ok := truthyValues[strings.ToLower(strings.TrimSpace(value))]
	return ok
}

// isStableRef reports whether the triggering git ref is a stable release: a
// pushed tag whose name starts with "v". Every other ref (a branch commit, a
// manual dispatch with an empty or non-tag ref) is a rolling prerelease.
func isStableRef(gitRef, refName string) bool {
	return strings.HasPrefix(gitRef, "refs/tags/") && strings.HasPrefix(refName, "v")
}

// executeRelease runs the ordered release steps and stops on the first error.
func executeRelease(cfg releaseConfig) error {
	if err := os.MkdirAll(cfg.distDir, 0o755); err != nil {
		return err
	}
	if err := checkCgoStub(); err != nil {
		return err
	}
	if err := pushReleaseTag(cfg); err != nil {
		return err
	}
	for _, platform := range cfg.platforms {
		if err := buildPlatform(cfg, platform); err != nil {
			return err
		}
	}
	if err := signDarwinBinaries(cfg); err != nil {
		return err
	}
	archives, err := archivePlatforms(cfg)
	if err != nil {
		return err
	}
	checksums, err := writeChecksums(cfg, archives)
	if err != nil {
		return err
	}
	return publishRelease(cfg, append(archives, checksums))
}

// pushReleaseTag creates the computed prerelease tag at the target commit and
// pushes it to origin under the github-actions bot identity. A stable release
// reuses the v-tag that already triggered the run, so there is nothing to push.
func pushReleaseTag(cfg releaseConfig) error {
	if !cfg.prerelease {
		return nil
	}
	if err := runProcess("git", []string{"config", "user.name", "github-actions[bot]"}, nil); err != nil {
		return err
	}
	if err := runProcess("git", []string{"config", "user.email", "github-actions[bot]@users.noreply.github.com"}, nil); err != nil {
		return err
	}
	if err := runProcess("git", []string{"tag", cfg.tag, cfg.targetSHA}, nil); err != nil {
		return err
	}
	return runProcess("git", []string{"push", "origin", cfg.tag}, nil)
}

// buildPlatform compiles one os/arch target with the stamped ldflags, writing
// the binary under dist/<binary>_<os>_<arch>/<binary>. When a consumer declared
// GO_MK_CGO_DEPS it first provisions that target's cgo dependencies and threads
// the per-target pkg-config directory into the build environment.
func buildPlatform(cfg releaseConfig, platform string) error {
	osName, arch, ok := strings.Cut(platform, "/")
	if !ok {
		return fmt.Errorf("release: malformed platform %q (want os/arch)", platform)
	}
	pkgConfigDir, err := provisionCgoDeps(osName, arch)
	if err != nil {
		return err
	}
	env := buildPlatformEnv(osName, arch, pkgConfigDir, os.Getenv("PKG_CONFIG_PATH"))
	for _, binary := range releaseBinaries(cfg) {
		if err := buildReleaseBinary(cfg, binary, osName, arch, env); err != nil {
			return err
		}
	}
	return nil
}

func buildReleaseBinary(cfg releaseConfig, binary releaseBinary, osName string, arch string, env []string) error {
	outDir := filepath.Join(cfg.distDir, fmt.Sprintf("%s_%s_%s", binary.name, osName, arch))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	outPath := filepath.Join(outDir, binary.name)
	args := []string{"build", "-trimpath", "-ldflags", releaseLdflags(cfg), "-o", outPath, binary.mainPkg}
	return runProcess("go", args, env)
}

// cgoDepsTarget is the make target that provisions the external C libraries a
// consumer declares through GO_MK_CGO_DEPS.
const cgoDepsTarget = "go-mk-cgo-deps"

// cgoCacheStampFile records the manifest key that produced the restored cgo
// dependency prefix.
const cgoCacheStampFile = ".go-mk-cgo-cache-key"

// cgoPrefixForTarget returns the absolute per-target install prefix for
// provisioned cgo dependencies, keyed by os/arch so a darwin cross build and a
// linux native build never share artifacts. It mirrors the GO_MK_CGO_PREFIX
// default in go.mk (whose empty-tuple case falls back to the host os/arch;
// this function always receives an explicit tuple) and is the single source of
// truth the make hook is given.
func cgoPrefixForTarget(workDir, osName, arch string) string {
	return filepath.Join(workDir, ".make", "cgo", osName+"-"+arch)
}

// provisionCgoDeps runs the make cgo-deps hook for one build target and returns
// the pkg-config directory under that target's prefix. An empty GO_MK_CGO_DEPS is
// a complete no-op: it starts no process and returns an empty path, so the build
// environment is left unchanged.
func provisionCgoDeps(osName, arch string) (string, error) {
	if strings.TrimSpace(os.Getenv("GO_MK_CGO_DEPS")) == "" {
		return "", nil
	}
	workDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	prefix := cgoPrefixForTarget(workDir, osName, arch)
	pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")
	cacheKey := strings.TrimSpace(os.Getenv("GO_MK_CGO_CACHE_KEY"))
	if cgoDepsCacheRestored(prefix, pkgConfigDir, cacheKey) {
		slog.Info("release skip cgo deps from restored cache", slog.String("goos", osName), slog.String("goarch", arch))
		return pkgConfigDir, nil
	}
	slog.Info("release provision cgo deps", slog.String("goos", osName), slog.String("goarch", arch))
	env := []string{
		"GO_MK_TARGET_GOOS=" + osName,
		"GO_MK_TARGET_GOARCH=" + arch,
		"GO_MK_CGO_PREFIX=" + prefix,
	}
	// The dep build (for example a static darwin pcre2) needs the same cross
	// compiler as the target build, supplied per-target rather than globally.
	env = append(env, crossCompilerEnv()...)
	if err := releaseRunProcess("make", []string{cgoDepsTarget}, env); err != nil {
		return "", err
	}
	if err := writeCgoDepsCacheStamp(prefix, cacheKey); err != nil {
		return "", err
	}
	return pkgConfigDir, nil
}

func cgoDepsCacheRestored(prefix, pkgConfigDir, cacheKey string) bool {
	if strings.TrimSpace(os.Getenv("GO_MK_CGO_CACHE_HIT")) != "true" {
		return false
	}
	if cacheKey == "" {
		return false
	}
	stamp, err := os.ReadFile(filepath.Join(prefix, cgoCacheStampFile))
	if err != nil {
		return false
	}
	// Trim surrounding whitespace so a stamp with a trailing newline (for
	// example one rewritten by a shell script) still compares equal and does
	// not force an unnecessary cold rebuild.
	if strings.TrimSpace(string(stamp)) != cacheKey {
		return false
	}
	info, err := os.Stat(pkgConfigDir)
	return err == nil && info.IsDir()
}

func writeCgoDepsCacheStamp(prefix, cacheKey string) error {
	slog.Info("release write cgo deps cache stamp", slog.String("prefix", prefix))
	if cacheKey == "" {
		return nil
	}
	info, err := os.Stat(prefix)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return os.WriteFile(filepath.Join(prefix, cgoCacheStampFile), []byte(cacheKey), 0o644)
}

// crossCompilerEnv returns the CC/CXX the workflow supplies for a cross build
// through GO_MK_CC and GO_MK_CXX. A darwin cross build sets them to the osxcross
// compiler; a native build leaves them empty and inherits the host toolchain.
// They are kept out of the global environment on purpose, so a host-tool build
// in the same job (the go-mk binary itself, quill) is never compiled with the
// target's cross compiler; only the target build and its cgo-dep provisioning
// receive them.
func crossCompilerEnv() []string {
	env := make([]string, 0, 2)
	if cc := strings.TrimSpace(os.Getenv("GO_MK_CC")); cc != "" {
		env = append(env, "CC="+cc)
	}
	if cxx := strings.TrimSpace(os.Getenv("GO_MK_CXX")); cxx != "" {
		env = append(env, "CXX="+cxx)
	}
	return env
}

// buildPlatformEnv returns the environment for a `go build` of one os/arch
// target. It applies the cross compiler for a cross build, and when pkgConfigDir
// is non-empty it prepends that per-target pkg-config directory to any inherited
// PKG_CONFIG_PATH so a consumer's `#cgo pkg-config` resolves a provisioned cgo
// dependency. An empty pkgConfigDir and no cross compiler leave the environment
// untouched, which keeps a pure-Go release byte-identical.
func buildPlatformEnv(osName, arch, pkgConfigDir, inheritedPkgConfigPath string) []string {
	env := []string{"CGO_ENABLED=" + cgoEnabledValue(), "GOOS=" + osName, "GOARCH=" + arch}
	env = append(env, crossCompilerEnv()...)
	if pkgConfigDir == "" {
		return env
	}
	pkgConfigPath := pkgConfigDir
	if strings.TrimSpace(inheritedPkgConfigPath) != "" {
		pkgConfigPath = pkgConfigDir + string(os.PathListSeparator) + inheritedPkgConfigPath
	}
	return append(env, "PKG_CONFIG_PATH="+pkgConfigPath)
}

// cgoEnabledValue returns the CGO_ENABLED value for release builds, defaulting
// to "0" so a pure-Go consumer keeps reproducible cross-compiles. A consumer
// with a cgo dependency sets CGO_ENABLED=1 in the build job, where the native
// runner and the system libraries the workflow installed make cgo compilation
// possible.
func cgoEnabledValue() string {
	if value := strings.TrimSpace(os.Getenv("CGO_ENABLED")); value != "" {
		return value
	}
	return "0"
}

// releaseLdflags builds the -ldflags value, stamping the project version
// package and optionally the gklog version package. The value is one argument
// passed to `go build`, so its internal spaces are preserved by exec.
func releaseLdflags(cfg releaseConfig) string {
	parts := []string{"-s", "-w"}
	if cfg.versionPkg != "" {
		parts = append(parts,
			"-X", cfg.versionPkg+".Version="+cfg.tag,
			"-X", cfg.versionPkg+".Commit="+cfg.shortSHA,
			"-X", cfg.versionPkg+".Dirty=false",
			"-X", cfg.versionPkg+".BuildTime="+cfg.buildTime,
		)
	}
	if cfg.gklogPkg != "" {
		parts = append(parts,
			"-X", cfg.gklogPkg+".Version="+cfg.tag,
			"-X", cfg.gklogPkg+".Commit="+cfg.shortSHA,
			"-X", cfg.gklogPkg+".Dirty=false",
			"-X", cfg.gklogPkg+".BuildTime="+cfg.buildTime,
			"-X", cfg.gklogPkg+".BinHash=",
		)
	}
	return strings.Join(parts, " ")
}

// hasDarwinPlatform reports whether any configured platform targets darwin, so
// a runner that built no darwin binary skips quill resolution entirely.
func hasDarwinPlatform(platforms []string) bool {
	for _, platform := range platforms {
		if osName, _, ok := strings.Cut(platform, "/"); ok && osName == "darwin" {
			return true
		}
	}
	return false
}

// signDarwinBinaries signs and notarizes every darwin binary with quill. It is
// skipped when no signing material is present so forks and dry runs still
// build. quill reads its credentials from the QUILL_* environment variables.
func signDarwinBinaries(cfg releaseConfig) error {
	if !hasDarwinPlatform(cfg.platforms) {
		return nil
	}
	if strings.TrimSpace(os.Getenv("QUILL_SIGN_P12")) == "" {
		if cfg.requireDarwinCodesign {
			return fmt.Errorf("release: darwin signing required but QUILL_SIGN_P12 is unset")
		}
		slog.Info("release skip signing", slog.String("reason", "QUILL_SIGN_P12 unset"))
		return nil
	}
	quill, err := resolveQuill()
	if err != nil {
		return err
	}
	for _, platform := range cfg.platforms {
		osName, arch, ok := strings.Cut(platform, "/")
		if !ok || osName != "darwin" {
			continue
		}
		for _, binary := range releaseBinaries(cfg) {
			bin := filepath.Join(cfg.distDir, fmt.Sprintf("%s_%s_%s", binary.name, osName, arch), binary.name)
			if err := signAndNotarizeDarwinBinary(quill, bin, cfg.entitlements); err != nil {
				return err
			}
		}
	}
	return nil
}

func signAndNotarizeDarwinBinary(quill string, bin string, entitlements string) error {
	args := []string{"sign-and-notarize", bin, "-vv"}
	if entitlements != "" {
		args = append(args, "--entitlements", entitlements)
	}
	var lastErr error
	for attempt := 1; attempt <= darwinSignAttempts; attempt++ {
		err := releaseRunProcess(quill, args, nil)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == darwinSignAttempts {
			break
		}
		slog.Warn(
			"release darwin sign-and-notarize failed; retrying",
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", darwinSignAttempts),
			slog.String("bin", bin),
			slog.Any("err", err),
		)
		releaseSleep(darwinSignRetryInterval)
	}
	return lastErr
}

// resolveQuill returns the quill executable path, preferring PATH and falling
// back to the Go install bin directory where `go install` places it.
func resolveQuill() (string, error) {
	if path, err := exec.LookPath("quill"); err == nil {
		return path, nil
	}
	gopath, err := goEnvPath("GOPATH")
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(gopath, "bin", "quill")
	if _, statErr := os.Stat(candidate); statErr != nil {
		return "", fmt.Errorf("release: quill not found on PATH or in %s", candidate)
	}
	return candidate, nil
}

// archivePlatforms writes one tar.gz per built platform containing the binary
// and a README.md when present, returning the archive paths.
func archivePlatforms(cfg releaseConfig) ([]string, error) {
	readme := ""
	if _, err := os.Stat("README.md"); err == nil {
		readme = "README.md"
	}
	binaries := releaseBinaries(cfg)
	archives := make([]string, 0, len(cfg.platforms)*len(binaries))
	for _, platform := range cfg.platforms {
		osName, arch, ok := strings.Cut(platform, "/")
		if !ok {
			return nil, fmt.Errorf("release: malformed platform %q", platform)
		}
		for _, binary := range binaries {
			name := fmt.Sprintf("%s_%s_%s", binary.name, osName, arch)
			dir := filepath.Join(cfg.distDir, name)
			archivePath := filepath.Join(cfg.distDir, name+".tar.gz")
			members := []tarMember{{source: filepath.Join(dir, binary.name), name: binary.name, mode: 0o755}}
			if readme != "" {
				members = append(members, tarMember{source: readme, name: "README.md", mode: 0o644})
			}
			if err := writeTarGz(archivePath, members); err != nil {
				return nil, err
			}
			archives = append(archives, archivePath)
		}
	}
	return archives, nil
}

func releaseBinaries(cfg releaseConfig) []releaseBinary {
	if len(cfg.binaries) > 0 {
		return cfg.binaries
	}
	if cfg.binary == "" {
		return nil
	}
	mainPackage := cfg.mainPkg
	if mainPackage == "" {
		mainPackage = "."
	}
	return []releaseBinary{{name: cfg.binary, mainPkg: mainPackage}}
}

// tarMember names a file to place into an archive at a fixed in-archive name.
type tarMember struct {
	source string
	name   string
	mode   int64
}

// writeTarGz writes the given members into a gzip-compressed tar at path.
func writeTarGz(path string, members []tarMember) error {
	slog.Info("release archive", slog.String("path", path), slog.Int("members", len(members)))
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, member := range members {
		if err := writeTarMember(tarWriter, member); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	return gzipWriter.Close()
}

// writeTarMember copies one member file into the open tar writer.
func writeTarMember(tarWriter *tar.Writer, member tarMember) error {
	content, err := os.ReadFile(member.source)
	if err != nil {
		return err
	}
	header := &tar.Header{Name: member.name, Mode: member.mode, Size: int64(len(content))}
	if err := tarWriter.WriteHeader(header); err != nil {
		return err
	}
	_, err = tarWriter.Write(content)
	return err
}

// writeChecksums writes a sha256 checksums file over the archives, formatted
// like sha256sum output, and returns its path.
func writeChecksums(cfg releaseConfig, archives []string) (string, error) {
	checksumsPath := filepath.Join(cfg.distDir, "checksums.txt")
	slog.Info("release checksums", slog.String("path", checksumsPath), slog.Int("archives", len(archives)))
	builder := strings.Builder{}
	for _, archive := range archives {
		sum, err := sha256File(archive)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&builder, "%s  %s\n", sum, filepath.Base(archive))
	}
	if err := os.WriteFile(checksumsPath, []byte(builder.String()), 0o644); err != nil {
		return "", err
	}
	return checksumsPath, nil
}

// sha256File returns the hex sha256 of the file at path.
func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// publishRelease creates the GitHub release with the given assets using gh.
func publishRelease(cfg releaseConfig, assets []string) error {
	args := []string{
		"release", "create", cfg.tag,
		"--target", cfg.targetSHA,
		"--title", cfg.binary + " " + cfg.tag,
		"--generate-notes",
	}
	if cfg.prerelease {
		args = append(args, "--prerelease")
	} else {
		args = append(args, "--latest")
	}
	args = append(args, assets...)
	return runProcess("gh", args, nil)
}

// runProcess runs name with args, streaming stdout and stderr, with extraEnv
// appended to the inherited environment. It is a process boundary, so it emits
// a structured slog event and returns the raw command error.
func runProcess(name string, args []string, extraEnv []string) error {
	slog.Info("release run process", slog.String("command", name), slog.Int("args", len(args)))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	return cmd.Run()
}

// gitOutput returns the trimmed stdout of a git command. It is a process
// boundary, so it emits a structured slog event.
func gitOutput(args ...string) (string, error) {
	return loggedGitOutput("release git", args...)
}

func loggedGitOutput(message string, args ...string) (string, error) {
	slog.Info(message, slog.Int("args", len(args)))
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
