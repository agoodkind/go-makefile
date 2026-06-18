// install orchestration for go-mk. It builds every binary a repo declares and
// installs each into its target directory, replacing the per-binary build and
// atomic-install shell that consumers otherwise copy by hand. The build flags,
// version stamping, and codesign inputs are assembled by the make layer and
// exported as environment values; this command consumes them, so the one place
// that knows ldflags stays the make layer. It owns process execution and file
// I/O; process boundaries emit a structured slog event and return the raw error
// for the caller to surface.
package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// binSpec is one binary to build and install: its installed name, its main
// package, and the directory it installs into.
type binSpec struct {
	name    string
	mainPkg string
	dir     string
}

// installConfig is the resolved install input, read once from the environment
// the make layer exports (BINARY, CMD, INSTALL_DIR, DIST_DIR, INSTALL_BINS, the
// GO_BUILD_* build flags, and the CODESIGN_* signing inputs).
type installConfig struct {
	bins                 []binSpec
	distDir              string
	buildTags            string
	ldflags              string
	extraFlags           []string
	codesignIdentity     string
	bundleID             string
	codesignTimestamp    string
	codesignEntitlements string
	installPreCommand    string
	installPostCommand   string
}

var (
	installOneFunc     = installOne
	runInstallHookFunc = runInstallHook
)

// runInstall runs the build gate, builds every declared binary, then installs
// each, returning the process exit code. The gate runs in-process locally and
// skips only when GitHub Actions proves the reusable CI gate job covers it.
func runInstall() int {
	if code := runBuildGate(); code != 0 {
		return code
	}
	cfg, err := loadInstallConfig()
	if err != nil {
		return statusFromError(err)
	}
	if err := buildAll(cfg); err != nil {
		return statusFromError(err)
	}
	return statusFromError(installAll(cfg))
}

// runBuild runs the build gate, then builds every declared binary without
// installing, returning the process exit code. The gate runs in-process locally
// and skips only when GitHub Actions proves the reusable CI gate job covers it.
func runBuild() int {
	if code := runBuildGate(); code != 0 {
		return code
	}
	cfg, err := loadInstallConfig()
	if err != nil {
		return statusFromError(err)
	}
	return statusFromError(buildAll(cfg))
}

// runUninstall removes every declared binary from its target directory,
// returning the process exit code.
func runUninstall() int {
	cfg, err := loadInstallConfig()
	if err != nil {
		return statusFromError(err)
	}
	for _, bin := range cfg.bins {
		if err := uninstallOne(bin); err != nil {
			return statusFromError(err)
		}
	}
	return 0
}

// loadInstallConfig resolves the install inputs from the environment.
func loadInstallConfig() (installConfig, error) {
	binary := strings.TrimSpace(os.Getenv("BINARY"))
	mainPkg := strings.TrimSpace(os.Getenv("CMD"))
	if mainPkg == "" {
		mainPkg = "."
	}
	installDir := strings.TrimSpace(os.Getenv("INSTALL_DIR"))
	if installDir == "" {
		installDir = defaultInstallDir()
	}
	distDir := strings.TrimSpace(os.Getenv("DIST_DIR"))
	if distDir == "" {
		distDir = "dist"
	}
	bins, err := parseInstallBins(os.Getenv("INSTALL_BINS"), binary, mainPkg, installDir)
	if err != nil {
		return installConfig{}, err
	}
	timestamp := strings.TrimSpace(os.Getenv("CODESIGN_TIMESTAMP"))
	if timestamp == "" {
		timestamp = "none"
	}
	return installConfig{
		bins:                 bins,
		distDir:              distDir,
		buildTags:            strings.TrimSpace(os.Getenv("GO_BUILD_TAGS")),
		ldflags:              strings.TrimSpace(os.Getenv("GO_BUILD_LDFLAGS")),
		extraFlags:           strings.Fields(os.Getenv("GO_BUILD_EXTRA_FLAGS")),
		codesignIdentity:     strings.TrimSpace(os.Getenv("CODESIGN_IDENTITY")),
		bundleID:             strings.TrimSpace(os.Getenv("BUNDLE_ID")),
		codesignTimestamp:    timestamp,
		codesignEntitlements: strings.TrimSpace(os.Getenv("CODESIGN_ENTITLEMENTS")),
		installPreCommand:    strings.TrimSpace(os.Getenv("GO_MK_INSTALL_PRE_CMD")),
		installPostCommand:   strings.TrimSpace(os.Getenv("GO_MK_INSTALL_POST_CMD")),
	}, nil
}

// defaultInstallDir mirrors the make default of XDG_BIN_HOME, then
// ~/.local/bin. The make layer normally exports INSTALL_DIR, so this is the
// fallback for a direct invocation.
func defaultInstallDir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_BIN_HOME")); xdg != "" {
		return xdg
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "bin")
}

// parseInstallBins parses the INSTALL_BINS declaration into one binSpec per
// entry. Each entry is name:cmd with an optional third field name:cmd:dir that
// overrides the install directory for that binary. An empty declaration falls
// back to the single BINARY:CMD installed into installDir, so a single-binary
// repo declares nothing.
func parseInstallBins(text, binary, mainPkg, installDir string) ([]binSpec, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		if binary == "" {
			return nil, errInstallNoBinary
		}
		return []binSpec{{name: binary, mainPkg: mainPkg, dir: installDir}}, nil
	}
	entries := strings.Fields(text)
	bins := make([]binSpec, 0, len(entries))
	for _, entry := range entries {
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return nil, &installBinError{entry: entry}
		}
		dir := installDir
		if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
			dir = parts[2]
		}
		bins = append(bins, binSpec{name: parts[0], mainPkg: parts[1], dir: dir})
	}
	return bins, nil
}

// buildAll builds every declared binary into the dist directory and prints one
// sign line for the whole run rather than one per binary.
func buildAll(cfg installConfig) error {
	if err := os.MkdirAll(cfg.distDir, 0o755); err != nil {
		return err
	}
	signed := 0
	for _, bin := range cfg.bins {
		didSign, err := buildOne(cfg, bin)
		if err != nil {
			return err
		}
		if didSign {
			signed++
		}
	}
	if signed > 0 {
		slog.Info("install signed binaries", slog.Int("count", signed))
		writeStdout("codesign   ok\n")
	}
	return nil
}

// buildOne compiles one binary into dist/<name> with the assembled build flags,
// then signs it on macOS, returning whether it signed. CGO and GOOS/GOARCH are
// inherited from the environment so the repo's own settings apply.
func buildOne(cfg installConfig, bin binSpec) (bool, error) {
	out := filepath.Join(cfg.distDir, bin.name)
	args := []string{"build"}
	if cfg.buildTags != "" {
		args = append(args, "-tags", cfg.buildTags)
	}
	if cfg.ldflags != "" {
		args = append(args, "-ldflags", cfg.ldflags)
	}
	args = append(args, cfg.extraFlags...)
	args = append(args, "-o", out, bin.mainPkg)
	if err := runProcess("go", args, nil); err != nil {
		return false, err
	}
	return signBinary(cfg, out)
}

func installAll(cfg installConfig) error {
	if err := runInstallHookFunc("pre", cfg.installPreCommand); err != nil {
		return err
	}
	var installErr error
	for _, bin := range cfg.bins {
		if err := installOneFunc(cfg, bin); err != nil {
			installErr = err
			break
		}
	}
	if err := runInstallHookFunc("post", cfg.installPostCommand); err != nil {
		if installErr != nil {
			slog.Error("install post hook failed after install failure", "install_err", installErr, "err", err)
			return errors.Join(installErr, err)
		}
		return err
	}
	return installErr
}

func runInstallHook(label string, command string) error {
	if command == "" {
		return nil
	}
	slog.Info("install hook", slog.String("hook", label))
	return runProcess("sh", []string{"-c", command}, nil)
}

// signBinary signs one binary with codesign on macOS, returning whether it
// signed so the caller prints one sign line per run rather than one per binary.
// The verify runs without --verbose so a multi-binary run does not stream a
// block per binary. It is a no-op on every other platform, so a Linux-only or
// cross-platform repo builds with no signing step. An empty identity on macOS is
// an error, matching the former make macro.
func signBinary(cfg installConfig, bin string) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, nil
	}
	if cfg.codesignIdentity == "" {
		return false, errNoCodesignIdentity
	}
	args := []string{
		"--force", "--sign", cfg.codesignIdentity,
		"--identifier", cfg.bundleID,
		"--options", "runtime",
		"--timestamp=" + cfg.codesignTimestamp,
	}
	if cfg.codesignEntitlements != "" {
		args = append(args, "--entitlements", cfg.codesignEntitlements)
	}
	args = append(args, bin)
	if err := runProcess("codesign", args, nil); err != nil {
		return false, err
	}
	if err := runProcess("codesign", []string{"--verify", bin}, nil); err != nil {
		return false, err
	}
	return true, nil
}

// installOne installs one built binary into its target directory. When the
// directory is writable it copies through a temporary file and renames so a
// partial copy never lands. When the directory is not writable it installs
// through sudo, so a system path such as /opt/scripts works without changing
// the caller.
func installOne(cfg installConfig, bin binSpec) error {
	src := filepath.Join(cfg.distDir, bin.name)
	target := filepath.Join(bin.dir, bin.name)
	if dirWritable(bin.dir) {
		if err := os.MkdirAll(bin.dir, 0o755); err != nil {
			return err
		}
		return atomicInstall(src, target)
	}
	slog.Info("install via sudo", slog.String("dir", bin.dir), slog.String("name", bin.name))
	if err := runProcess("sudo", []string{"install", "-d", bin.dir}, nil); err != nil {
		return err
	}
	return runProcess("sudo", []string{"install", "-m", "0755", src, target}, nil)
}

// uninstallOne removes one installed binary, using sudo when its directory is
// not writable. A missing binary is not an error.
func uninstallOne(bin binSpec) error {
	target := filepath.Join(bin.dir, bin.name)
	slog.Info("uninstall binary", slog.String("target", target))
	if dirWritable(bin.dir) {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return runProcess("sudo", []string{"rm", "-f", target}, nil)
}

// atomicInstall copies src into target through a temporary file in the target
// directory and renames it into place with mode 0755. It writes files, so it
// emits a boundary log.
func atomicInstall(src, target string) error {
	slog.Info("install binary", slog.String("target", target))
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".new")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	if _, err := io.Copy(temporary, source); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
		return err
	}
	if err := temporary.Chmod(0o755); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryName)
		return err
	}
	if err := os.Rename(temporaryName, target); err != nil {
		_ = os.Remove(temporaryName)
		return err
	}
	return nil
}

// dirWritable reports whether a file can be created in dir. It walks up to the
// nearest existing ancestor and tries to create a probe file there, so a
// not-yet-created directory under a writable parent counts as writable. It
// reads and writes the filesystem, so it emits a boundary log.
func dirWritable(dir string) bool {
	slog.Info("install probe writable", slog.String("dir", dir))
	existing := dir
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return false
		}
		existing = parent
	}
	probe, err := os.CreateTemp(existing, ".gomk-write-test")
	if err != nil {
		return false
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return true
}

const errInstallNoBinary sentinelError = "install: BINARY is not set and INSTALL_BINS is empty"

const errNoCodesignIdentity sentinelError = "install: no codesign identity on macOS; set CERT_ID in config.mk or install a Developer ID Application certificate"

// installBinError reports a malformed INSTALL_BINS entry.
type installBinError struct {
	entry string
}

func (errorValue *installBinError) Error() string {
	return "malformed INSTALL_BINS entry " + errorValue.entry + " (want name:cmd or name:cmd:dir)"
}
