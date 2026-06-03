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
	"strconv"
	"strings"
	"time"
)

// defaultReleasePlatforms is the os/arch matrix built when RELEASE_PLATFORMS is
// unset. CGO is disabled, so darwin binaries cross-compile from any host.
const defaultReleasePlatforms = "darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"

// releaseConfig is the resolved release input, read once from the environment
// that the make layer exports (BINARY, CMD, VPKG, GKLOG_VPKG, RELEASE_*).
type releaseConfig struct {
	binary       string
	mainPkg      string
	versionPkg   string
	gklogPkg     string
	entitlements string
	platforms    []string
	distDir      string
	tag          string
	shortSHA     string
	targetSHA    string
	buildTime    string
}

// runRelease loads the release configuration and runs the full pipeline,
// returning the process exit code.
func runRelease() int {
	cfg, err := loadReleaseConfig()
	if err != nil {
		return statusFromError(err)
	}
	return statusFromError(executeRelease(cfg))
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
	tag := fmt.Sprintf("%s-%s-%s", now.Format("200601021504"), hexRun, shortSHA)

	return releaseConfig{
		binary:       binary,
		mainPkg:      mainPkg,
		versionPkg:   strings.TrimSpace(os.Getenv("VPKG")),
		gklogPkg:     strings.TrimSpace(os.Getenv("GKLOG_VPKG")),
		entitlements: strings.TrimSpace(os.Getenv("RELEASE_ENTITLEMENTS")),
		platforms:    strings.Fields(platformsText),
		distDir:      distDir,
		tag:          tag,
		shortSHA:     shortSHA,
		targetSHA:    targetSHA,
		buildTime:    now.Format("2006-01-02T15:04:05Z"),
	}, nil
}

// executeRelease runs the ordered release steps and stops on the first error.
func executeRelease(cfg releaseConfig) error {
	if err := os.MkdirAll(cfg.distDir, 0o755); err != nil {
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

// pushReleaseTag creates the computed tag at the target commit and pushes it to
// origin under the github-actions bot identity.
func pushReleaseTag(cfg releaseConfig) error {
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

// buildPlatform compiles one os/arch target with CGO disabled and the stamped
// ldflags, writing the binary under dist/<binary>_<os>_<arch>/<binary>.
func buildPlatform(cfg releaseConfig, platform string) error {
	osName, arch, ok := strings.Cut(platform, "/")
	if !ok {
		return fmt.Errorf("release: malformed platform %q (want os/arch)", platform)
	}
	outDir := filepath.Join(cfg.distDir, fmt.Sprintf("%s_%s_%s", cfg.binary, osName, arch))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	outPath := filepath.Join(outDir, cfg.binary)
	args := []string{"build", "-trimpath", "-ldflags", releaseLdflags(cfg), "-o", outPath, cfg.mainPkg}
	env := []string{"CGO_ENABLED=0", "GOOS=" + osName, "GOARCH=" + arch}
	return runProcess("go", args, env)
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
			"-X", cfg.gklogPkg+".Commit="+cfg.shortSHA,
			"-X", cfg.gklogPkg+".Dirty=false",
			"-X", cfg.gklogPkg+".BuildTime="+cfg.buildTime,
			"-X", cfg.gklogPkg+".BinHash=",
		)
	}
	return strings.Join(parts, " ")
}

// signDarwinBinaries signs and notarizes every darwin binary with quill. It is
// skipped when no signing material is present so forks and dry runs still
// build. quill reads its credentials from the QUILL_* environment variables.
func signDarwinBinaries(cfg releaseConfig) error {
	if strings.TrimSpace(os.Getenv("QUILL_SIGN_P12")) == "" {
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
		bin := filepath.Join(cfg.distDir, fmt.Sprintf("%s_%s_%s", cfg.binary, osName, arch), cfg.binary)
		args := []string{"sign-and-notarize", bin, "-vv"}
		if cfg.entitlements != "" {
			args = append(args, "--entitlements", cfg.entitlements)
		}
		if err := runProcess(quill, args, nil); err != nil {
			return err
		}
	}
	return nil
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
	archives := make([]string, 0, len(cfg.platforms))
	for _, platform := range cfg.platforms {
		osName, arch, ok := strings.Cut(platform, "/")
		if !ok {
			return nil, fmt.Errorf("release: malformed platform %q", platform)
		}
		name := fmt.Sprintf("%s_%s_%s", cfg.binary, osName, arch)
		dir := filepath.Join(cfg.distDir, name)
		archivePath := filepath.Join(cfg.distDir, name+".tar.gz")
		members := []tarMember{{source: filepath.Join(dir, cfg.binary), name: cfg.binary, mode: 0o755}}
		if readme != "" {
			members = append(members, tarMember{source: readme, name: "README.md", mode: 0o644})
		}
		if err := writeTarGz(archivePath, members); err != nil {
			return nil, err
		}
		archives = append(archives, archivePath)
	}
	return archives, nil
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
	slog.Info("release git", slog.Int("args", len(args)))
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
