package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIsStableRef(t *testing.T) {
	cases := []struct {
		name    string
		gitRef  string
		refName string
		want    bool
	}{
		{name: "semver tag is stable", gitRef: "refs/tags/v0.1.0", refName: "v0.1.0", want: true},
		{name: "v-prefixed tag is stable", gitRef: "refs/tags/v1", refName: "v1", want: true},
		{name: "main branch is prerelease", gitRef: "refs/heads/main", refName: "main", want: false},
		{name: "non-v tag is prerelease", gitRef: "refs/tags/2026.06.03", refName: "2026.06.03", want: false},
		{name: "empty ref is prerelease", gitRef: "", refName: "", want: false},
		{name: "v branch name without tag ref is prerelease", gitRef: "refs/heads/victory", refName: "victory", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := isStableRef(testCase.gitRef, testCase.refName)
			if got != testCase.want {
				t.Fatalf("isStableRef(%q, %q) = %v, want %v", testCase.gitRef, testCase.refName, got, testCase.want)
			}
		})
	}
}

func TestEnvTruthy(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "one", value: "1", want: true},
		{name: "true", value: "true", want: true},
		{name: "yes", value: "yes", want: true},
		{name: "on", value: "on", want: true},
		{name: "empty", value: "", want: false},
		{name: "zero", value: "0", want: false},
		{name: "false", value: "false", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := envTruthy(testCase.value); got != testCase.want {
				t.Fatalf("envTruthy(%q) = %v, want %v", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestLoadReleaseConfigDefaultsToSingleBinary(t *testing.T) {
	t.Setenv("BINARY", "agent-gate")
	t.Setenv("CMD", "")
	t.Setenv("RELEASE_BINS", "")
	t.Setenv("RELEASE_PLATFORMS", "linux/amd64")

	cfg, err := loadReleaseConfig()
	if err != nil {
		t.Fatalf("loadReleaseConfig() error: %v", err)
	}
	if cfg.binary != "agent-gate" {
		t.Fatalf("binary = %q, want agent-gate", cfg.binary)
	}
	if cfg.mainPkg != "." {
		t.Fatalf("mainPkg = %q, want .", cfg.mainPkg)
	}
	wantBinaries := []releaseBinary{{name: "agent-gate", mainPkg: "."}}
	if !slices.Equal(cfg.binaries, wantBinaries) {
		t.Fatalf("binaries = %#v, want %#v", cfg.binaries, wantBinaries)
	}
}

func TestLoadReleaseConfigParsesReleaseBins(t *testing.T) {
	t.Setenv("BINARY", "agent-gate")
	t.Setenv("CMD", "./cmd/agent-gate")
	t.Setenv("RELEASE_BINS", "agent-gate:./cmd/agent-gate agentctl:./cmd/agentctl")
	t.Setenv("RELEASE_PLATFORMS", "linux/amd64")

	cfg, err := loadReleaseConfig()
	if err != nil {
		t.Fatalf("loadReleaseConfig() error: %v", err)
	}
	wantBinaries := []releaseBinary{
		{name: "agent-gate", mainPkg: "./cmd/agent-gate"},
		{name: "agentctl", mainPkg: "./cmd/agentctl"},
	}
	if !slices.Equal(cfg.binaries, wantBinaries) {
		t.Fatalf("binaries = %#v, want %#v", cfg.binaries, wantBinaries)
	}
	if cfg.binary != "agent-gate" || cfg.mainPkg != "./cmd/agent-gate" {
		t.Fatalf("primary = %s %s, want agent-gate ./cmd/agent-gate", cfg.binary, cfg.mainPkg)
	}
}

func TestLoadReleaseConfigRejectsMalformedReleaseBins(t *testing.T) {
	t.Setenv("BINARY", "agent-gate")
	t.Setenv("CMD", "./cmd/agent-gate")
	t.Setenv("RELEASE_BINS", "agent-gate:./cmd/agent-gate malformed")

	_, err := loadReleaseConfig()
	if err == nil {
		t.Fatal("loadReleaseConfig() error = nil, want malformed RELEASE_BINS error")
	}
	if !strings.Contains(err.Error(), `release: malformed RELEASE_BINS entry "malformed"`) {
		t.Fatalf("loadReleaseConfig() error = %v", err)
	}
}

func TestLoadReleaseConfigReordersPrimaryBinaryFirst(t *testing.T) {
	t.Setenv("BINARY", "agent-gate")
	t.Setenv("CMD", "./cmd/agent-gate")
	t.Setenv("RELEASE_BINS", "agentctl:./cmd/agentctl agent-gate:./cmd/agent-gate helper:./cmd/helper")
	t.Setenv("RELEASE_PLATFORMS", "linux/amd64")

	cfg, err := loadReleaseConfig()
	if err != nil {
		t.Fatalf("loadReleaseConfig() error: %v", err)
	}
	wantBinaries := []releaseBinary{
		{name: "agent-gate", mainPkg: "./cmd/agent-gate"},
		{name: "agentctl", mainPkg: "./cmd/agentctl"},
		{name: "helper", mainPkg: "./cmd/helper"},
	}
	if !slices.Equal(cfg.binaries, wantBinaries) {
		t.Fatalf("binaries = %#v, want %#v", cfg.binaries, wantBinaries)
	}
	if cfg.binary != "agent-gate" || cfg.mainPkg != "./cmd/agent-gate" {
		t.Fatalf("primary = %s %s, want agent-gate ./cmd/agent-gate", cfg.binary, cfg.mainPkg)
	}
}

func TestLoadReleaseConfigRejectsReleaseBinsWithoutPrimary(t *testing.T) {
	t.Setenv("BINARY", "agent-gate")
	t.Setenv("CMD", "./cmd/agent-gate")
	t.Setenv("RELEASE_BINS", "agentctl:./cmd/agentctl helper:./cmd/helper")

	_, err := loadReleaseConfig()
	if err == nil {
		t.Fatal("loadReleaseConfig() error = nil, want missing primary binary error")
	}
	if !strings.Contains(err.Error(), `release: RELEASE_BINS must include the primary binary "agent-gate"`) {
		t.Fatalf("loadReleaseConfig() error = %v", err)
	}
}

func TestArchivePlatformsWritesOneArchivePerBinaryAndPlatform(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	distDir := "dist"
	binaries := []releaseBinary{
		{name: "agent-gate", mainPkg: "./cmd/agent-gate"},
		{name: "agentctl", mainPkg: "./cmd/agentctl"},
	}
	platforms := []string{"darwin/arm64", "linux/amd64"}
	for _, binary := range binaries {
		for _, platform := range platforms {
			osName, arch, ok := strings.Cut(platform, "/")
			if !ok {
				t.Fatalf("bad test platform %q", platform)
			}
			outDir := filepath.Join(distDir, binary.name+"_"+osName+"_"+arch)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", outDir, err)
			}
			outPath := filepath.Join(outDir, binary.name)
			if err := os.WriteFile(outPath, []byte(binary.name), 0o755); err != nil {
				t.Fatalf("write %s: %v", outPath, err)
			}
		}
	}

	archives, err := archivePlatforms(releaseConfig{
		binary:    "agent-gate",
		binaries:  binaries,
		platforms: platforms,
		distDir:   distDir,
	})
	if err != nil {
		t.Fatalf("archivePlatforms() error: %v", err)
	}
	wantArchives := []string{
		filepath.Join(distDir, "agent-gate_darwin_arm64.tar.gz"),
		filepath.Join(distDir, "agentctl_darwin_arm64.tar.gz"),
		filepath.Join(distDir, "agent-gate_linux_amd64.tar.gz"),
		filepath.Join(distDir, "agentctl_linux_amd64.tar.gz"),
	}
	if !slices.Equal(archives, wantArchives) {
		t.Fatalf("archives = %#v, want %#v", archives, wantArchives)
	}
	for _, archive := range wantArchives {
		if _, err := os.Stat(archive); err != nil {
			t.Fatalf("stat %s: %v", archive, err)
		}
	}
}

func TestReleaseLdflagsStampsGklogVersion(t *testing.T) {
	flags := releaseLdflags(releaseConfig{
		gklogPkg:  "goodkind.io/gklog/version",
		tag:       "v1.2.3",
		shortSHA:  "abc1234",
		buildTime: "2026-07-02T00:00:00Z",
	})
	if !strings.Contains(flags, "-X goodkind.io/gklog/version.Version=v1.2.3") {
		t.Fatalf("releaseLdflags() = %q, want gklog Version stamp", flags)
	}
}

func TestGoBuildMkStampsGklogVersionForLocalBuilds(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not available")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	workDir := t.TempDir()
	makefile := "BINARY := demo\n" +
		"CMD := ./cmd/demo\n" +
		"GKLOG_VPKG := goodkind.io/gklog/version\n" +
		"include " + filepath.Join(repoRoot, "go-build.mk") + "\n\n" +
		"print-ldflags:\n" +
		"\t@printf '%s\\n' '$(GO_BUILD_LDFLAGS)'\n"
	if err := os.WriteFile(filepath.Join(workDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	cmd := exec.Command(makeBin, "print-ldflags")
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make print-ldflags failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "-X goodkind.io/gklog/version.Version=dev") {
		t.Fatalf("GO_BUILD_LDFLAGS = %q, want gklog Version=dev", output)
	}
}

func TestGoReleaseMkExportsReleaseBins(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not available")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	workDir := t.TempDir()
	makefile := fmt.Sprintf(`BINARY := demo
CMD := ./cmd/demo
RELEASE_BINS := demo:./cmd/demo helper:./cmd/helper
include %s

print-release-bins:
	@printf '%%s\n' "$$RELEASE_BINS"
`, filepath.Join(repoRoot, "go-release.mk"))
	if err := os.WriteFile(filepath.Join(workDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	cmd := exec.Command(makeBin, "--no-print-directory", "print-release-bins")
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make print-release-bins failed: %v\n%s", err, output)
	}
	// GNU make prints "Entering/Leaving directory" lines under recursion even
	// with --no-print-directory in some versions, so assert the RELEASE_BINS
	// value appears on its own line rather than exact-matching the whole output.
	want := "demo:./cmd/demo helper:./cmd/helper"
	found := false
	for _, line := range strings.Split(string(output), "\n") {
		if line == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RELEASE_BINS line %q not found in recipe output %q", want, output)
	}
}

func TestSignDarwinBinariesRequiresSigningWhenConfigured(t *testing.T) {
	t.Setenv("QUILL_SIGN_P12", "")
	err := signDarwinBinaries(releaseConfig{
		requireDarwinCodesign: true,
		platforms:             []string{"darwin/arm64"},
	})
	if err == nil {
		t.Fatal("signDarwinBinaries() = nil, want error")
	}
	if err.Error() != "release: darwin signing required but QUILL_SIGN_P12 is unset" {
		t.Fatalf("signDarwinBinaries() error = %v", err)
	}
}

func TestSignDarwinBinariesSkipsWhenNoDarwinTargets(t *testing.T) {
	t.Setenv("QUILL_SIGN_P12", "")
	if err := signDarwinBinaries(releaseConfig{
		requireDarwinCodesign: true,
		platforms:             []string{"linux/amd64"},
	}); err != nil {
		t.Fatalf("signDarwinBinaries() error = %v, want nil", err)
	}
}

func TestSignAndNotarizeDarwinBinaryRetriesThenSucceeds(t *testing.T) {
	originalRunProcess := releaseRunProcess
	originalSleep := releaseSleep
	originalAttempts := darwinSignAttempts
	originalDelay := darwinSignRetryInterval
	t.Cleanup(func() {
		releaseRunProcess = originalRunProcess
		releaseSleep = originalSleep
		darwinSignAttempts = originalAttempts
		darwinSignRetryInterval = originalDelay
	})

	callCount := 0
	releaseRunProcess = func(_ string, _ []string, _ []string) error {
		callCount++
		if callCount < 3 {
			return errStubRetry
		}
		return nil
	}
	releaseSleep = func(_ time.Duration) {}
	darwinSignAttempts = 3
	darwinSignRetryInterval = 0

	if err := signAndNotarizeDarwinBinary("quill", "dist/agent-gate", ""); err != nil {
		t.Fatalf("signAndNotarizeDarwinBinary() error = %v, want nil", err)
	}
	if callCount != 3 {
		t.Fatalf("callCount = %d, want 3", callCount)
	}
}

func TestSignAndNotarizeDarwinBinaryReturnsLastError(t *testing.T) {
	quill := relProcQuill(t)
	workDir := relProcEnvironment(t)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "QUILL_") {
			t.Setenv(key, "")
		}
	}
	binPath := filepath.Join(workDir, "not-macho")
	if err := os.WriteFile(binPath, []byte("not a Mach-O binary\n"), 0o600); err != nil {
		t.Fatalf("write signing input: %v", err)
	}
	relProcDisableRetryDelay(t)
	var logs bytes.Buffer
	originalLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	err := signAndNotarizeDarwinBinary(quill, binPath, "")
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("signAndNotarizeDarwinBinary() error = %v, want process exit error", err)
	}
	if exitError.ExitCode() != 1 {
		t.Fatalf("quill exit code = %d, want 1", exitError.ExitCode())
	}
	if got := strings.Count(logs.String(), `msg="release run process"`); got != darwinSignAttempts {
		t.Fatalf("quill attempts = %d, want %d\n%s", got, darwinSignAttempts, logs.String())
	}
}

func TestCgoPrefixForTarget(t *testing.T) {
	got := cgoPrefixForTarget("/work", "darwin", "arm64")
	want := filepath.Join("/work", ".make", "cgo", "darwin-arm64")
	if got != want {
		t.Fatalf("cgoPrefixForTarget = %q, want %q", got, want)
	}
}

// TestBuildPlatformEnv proves the per-target pkg-config directory is prepended to
// any inherited PKG_CONFIG_PATH for a cgo build, and that a non-cgo build (empty
// pkgConfigDir) leaves PKG_CONFIG_PATH out of the environment entirely.
func TestBuildPlatformEnv(t *testing.T) {
	separator := string(os.PathListSeparator)
	cases := []struct {
		name         string
		pkgConfigDir string
		inherited    string
		wantPkg      string
	}{
		{name: "no cgo leaves env unchanged", pkgConfigDir: "", inherited: "", wantPkg: ""},
		{name: "no cgo ignores inherited path", pkgConfigDir: "", inherited: "/usr/lib/pkgconfig", wantPkg: ""},
		{name: "cgo without inherited", pkgConfigDir: "/p/lib/pkgconfig", inherited: "", wantPkg: "/p/lib/pkgconfig"},
		{name: "cgo prepends inherited", pkgConfigDir: "/p/lib/pkgconfig", inherited: "/usr/lib/pkgconfig", wantPkg: "/p/lib/pkgconfig" + separator + "/usr/lib/pkgconfig"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := buildPlatformEnv("linux", "amd64", testCase.pkgConfigDir, testCase.inherited)
			if !envContains(env, "CGO_ENABLED="+cgoEnabledValue()) {
				t.Fatalf("env missing CGO_ENABLED: %v", env)
			}
			if !envContains(env, "GOOS=linux") || !envContains(env, "GOARCH=amd64") {
				t.Fatalf("env missing GOOS/GOARCH: %v", env)
			}
			gotPkg := pkgConfigEntry(env)
			if testCase.wantPkg == "" {
				if gotPkg != "" {
					t.Fatalf("PKG_CONFIG_PATH = %q, want absent", gotPkg)
				}
				return
			}
			if gotPkg != "PKG_CONFIG_PATH="+testCase.wantPkg {
				t.Fatalf("PKG_CONFIG_PATH = %q, want %q", gotPkg, "PKG_CONFIG_PATH="+testCase.wantPkg)
			}
		})
	}
}

func TestProvisionCgoDepsNoOpWhenUnset(t *testing.T) {
	fixture := relProcCgoFixture(t)
	t.Setenv("GO_MK_CGO_DEPS", "")
	dir, err := provisionCgoDeps("linux", "amd64")
	if err != nil {
		t.Fatalf("provisionCgoDeps error = %v, want nil", err)
	}
	if dir != "" {
		t.Fatalf("provisionCgoDeps dir = %q, want empty", dir)
	}
	relProcAssertHook(t, fixture, false)
}

func TestProvisionCgoDepsRunsHookAndComposesEnv(t *testing.T) {
	fixture := relProcCgoFixture(t)
	t.Setenv("GO_MK_CC", "target-cc")
	t.Setenv("GO_MK_CXX", "target-cxx")
	t.Setenv("CC", "host-cc")
	t.Setenv("CXX", "host-cxx")
	dir, err := provisionCgoDeps("darwin", "arm64")
	if err != nil {
		t.Fatalf("provisionCgoDeps error = %v", err)
	}
	wantPrefix := filepath.Join(fixture.workDir, ".make", "cgo", "darwin-arm64")
	wantDir := filepath.Join(wantPrefix, "lib", "pkgconfig")
	if dir != wantDir {
		t.Fatalf("provisionCgoDeps dir = %q, want %q", dir, wantDir)
	}
	relProcAssertHook(t, fixture, true)
	wantEnv := strings.Join([]string{
		"GO_MK_TARGET_GOOS=darwin",
		"GO_MK_TARGET_GOARCH=arm64",
		"GO_MK_CGO_PREFIX=" + wantPrefix,
		"CC=target-cc", "CXX=target-cxx", "",
	}, "\n")
	if got := relProcReadFile(t, fixture.envPath); got != wantEnv {
		t.Fatalf("hook environment = %q, want %q", got, wantEnv)
	}
	if got := relProcReadFile(t, filepath.Join(dir, "demolib.pc")); got != "Name: demolib\n" {
		t.Fatalf("pkg-config file = %q, want demolib metadata", got)
	}
}

func TestProvisionCgoDepsSkipsWarmCache(t *testing.T) {
	engine, fixture := relProcCgoCommandFixture(t)
	t.Setenv("GO_MK_CGO_CACHE_HIT", "true")
	t.Setenv("GO_MK_CGO_CACHE_KEY", "cache-key-1")
	prefix := filepath.Join(fixture.workDir, ".make", "cgo", "darwin-arm64")
	pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")
	if err := os.MkdirAll(pkgConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg-config dir: %v", err)
	}
	stampPath := filepath.Join(prefix, ".go-mk-cgo-cache-key")
	if err := os.WriteFile(stampPath, []byte("cache-key-1"), 0o644); err != nil {
		t.Fatalf("write stamp: %v", err)
	}

	relProcRunCgoCompile(t, engine, fixture, "darwin/arm64")
	relProcAssertHook(t, fixture, false)
	if got := relProcReadFile(t, stampPath); got != "cache-key-1" {
		t.Fatalf("stamp content = %q, want cache-key-1", got)
	}
}

func TestProvisionCgoDepsSkipsWarmCacheWithTrailingNewlineStamp(t *testing.T) {
	engine, fixture := relProcCgoCommandFixture(t)
	t.Setenv("GO_MK_CGO_CACHE_HIT", "true")
	t.Setenv("GO_MK_CGO_CACHE_KEY", "cache-key-1")
	prefix := filepath.Join(fixture.workDir, ".make", "cgo", "darwin-arm64")
	pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")
	if err := os.MkdirAll(pkgConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg-config dir: %v", err)
	}
	stampPath := filepath.Join(prefix, ".go-mk-cgo-cache-key")
	if err := os.WriteFile(stampPath, []byte("cache-key-1\n"), 0o644); err != nil {
		t.Fatalf("write stamp: %v", err)
	}

	relProcRunCgoCompile(t, engine, fixture, "darwin/arm64")
	relProcAssertHook(t, fixture, false)
	if got := relProcReadFile(t, stampPath); got != "cache-key-1\n" {
		t.Fatalf("stamp content = %q, want cache-key-1 with a newline", got)
	}
}

// TestRunReleaseStageRejectsUnknownStage proves an unrecognized RELEASE_STAGE
// fails with an error rather than falling through to the publishing all-in-one
// path, so a stray or version-skewed stage name never publishes a release.
func TestRunReleaseStageRejectsUnknownStage(t *testing.T) {
	err := runReleaseStage("frobnicate", releaseConfig{})
	if err == nil {
		t.Fatal("runReleaseStage() = nil, want error for unknown stage")
	}
	if !strings.Contains(err.Error(), `unknown RELEASE_STAGE "frobnicate"`) {
		t.Fatalf("runReleaseStage() error = %v, want unknown stage error", err)
	}
}

// TestPackageStageArchivesCompiledBinariesWithoutSigning proves the package
// stage archives the binaries the compile stage produced and skips signing when
// no signing material is present, so a dry run still produces one tar.gz per
// (binary, platform) pair without recompiling.
func TestPackageStageArchivesCompiledBinariesWithoutSigning(t *testing.T) {
	t.Setenv("QUILL_SIGN_P12", "")
	workDir := t.TempDir()
	t.Chdir(workDir)
	distDir := "dist"
	binaries := []releaseBinary{{name: "agent-gate", mainPkg: "."}}
	platforms := []string{"darwin/arm64", "linux/amd64"}
	for _, binary := range binaries {
		for _, platform := range platforms {
			osName, arch, ok := strings.Cut(platform, "/")
			if !ok {
				t.Fatalf("bad test platform %q", platform)
			}
			outDir := filepath.Join(distDir, binary.name+"_"+osName+"_"+arch)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", outDir, err)
			}
			if err := os.WriteFile(filepath.Join(outDir, binary.name), []byte(binary.name), 0o755); err != nil {
				t.Fatalf("write compiled binary: %v", err)
			}
		}
	}

	if err := packageStage(releaseConfig{
		binary:    "agent-gate",
		binaries:  binaries,
		platforms: platforms,
		distDir:   distDir,
	}); err != nil {
		t.Fatalf("packageStage() error = %v, want nil", err)
	}
	for _, want := range []string{
		filepath.Join(distDir, "agent-gate_darwin_arm64.tar.gz"),
		filepath.Join(distDir, "agent-gate_linux_amd64.tar.gz"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("stat %s: %v", want, err)
		}
	}
}

func TestProvisionCgoDepsRunsWhenWarmCacheConditionFails(t *testing.T) {
	testCases := []struct {
		name             string
		cacheHit         string
		cacheKey         string
		stampValue       string
		createStamp      bool
		createPkgConfig  bool
		wantStampContent string
	}{
		{
			name:             "cache_hit_is_false",
			cacheHit:         "false",
			cacheKey:         "cache-key-1",
			stampValue:       "cache-key-1",
			createStamp:      true,
			createPkgConfig:  true,
			wantStampContent: "cache-key-1",
		},
		{
			name:             "cache_key_is_empty",
			cacheHit:         "true",
			cacheKey:         "",
			stampValue:       "",
			createStamp:      true,
			createPkgConfig:  true,
			wantStampContent: "",
		},
		{
			name:             "stamp_is_missing",
			cacheHit:         "true",
			cacheKey:         "cache-key-1",
			createPkgConfig:  true,
			wantStampContent: "cache-key-1",
		},
		{
			name:             "stamp_mismatches",
			cacheHit:         "true",
			cacheKey:         "cache-key-1",
			stampValue:       "other-key",
			createStamp:      true,
			createPkgConfig:  true,
			wantStampContent: "cache-key-1",
		},
		{
			name:             "pkg_config_dir_is_missing",
			cacheHit:         "true",
			cacheKey:         "cache-key-1",
			stampValue:       "cache-key-1",
			createStamp:      true,
			wantStampContent: "cache-key-1",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			engine, fixture := relProcCgoCommandFixture(t)
			t.Setenv("GO_MK_CGO_CACHE_HIT", testCase.cacheHit)
			t.Setenv("GO_MK_CGO_CACHE_KEY", testCase.cacheKey)
			prefix := filepath.Join(fixture.workDir, ".make", "cgo", "darwin-arm64")
			pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")
			if testCase.createPkgConfig {
				if err := os.MkdirAll(pkgConfigDir, 0o755); err != nil {
					t.Fatalf("mkdir pkg-config dir: %v", err)
				}
			} else {
				if err := os.MkdirAll(prefix, 0o755); err != nil {
					t.Fatalf("mkdir prefix: %v", err)
				}
			}
			stampPath := filepath.Join(prefix, ".go-mk-cgo-cache-key")
			if testCase.createStamp {
				if err := os.MkdirAll(prefix, 0o755); err != nil {
					t.Fatalf("mkdir prefix: %v", err)
				}
				if err := os.WriteFile(stampPath, []byte(testCase.stampValue), 0o644); err != nil {
					t.Fatalf("write stamp: %v", err)
				}
			}

			relProcRunCgoCompile(t, engine, fixture, "darwin/arm64")
			relProcAssertHook(t, fixture, true)
			if got := relProcReadFile(t, filepath.Join(pkgConfigDir, "demolib.pc")); got != "Name: demolib\n" {
				t.Fatalf("pkg-config file = %q, want demolib metadata", got)
			}
			gotStamp, err := os.ReadFile(stampPath)
			if testCase.cacheKey == "" {
				if err != nil {
					t.Fatalf("read existing stamp: %v", err)
				}
			} else if err != nil {
				t.Fatalf("read stamp: %v", err)
			}
			if string(gotStamp) != testCase.wantStampContent {
				t.Fatalf("stamp content = %q, want %q", string(gotStamp), testCase.wantStampContent)
			}
		})
	}
}

func TestProvisionCgoDepsWritesStampAfterSuccess(t *testing.T) {
	engine, fixture := relProcCgoCommandFixture(t)
	t.Setenv("GO_MK_CGO_CACHE_KEY", "cache-key-1")
	prefix := filepath.Join(fixture.workDir, ".make", "cgo", "linux-amd64")

	relProcRunCgoCompile(t, engine, fixture, "linux/amd64")
	relProcAssertHook(t, fixture, true)
	stampPath := filepath.Join(prefix, ".go-mk-cgo-cache-key")
	gotStamp, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	if string(gotStamp) != "cache-key-1" {
		t.Fatalf("stamp content = %q, want cache-key-1", string(gotStamp))
	}
}

func TestProvisionCgoDepsDoesNotWriteStampWhenKeyIsEmpty(t *testing.T) {
	engine, fixture := relProcCgoCommandFixture(t)
	t.Setenv("GO_MK_CGO_CACHE_KEY", "")
	prefix := filepath.Join(fixture.workDir, ".make", "cgo", "linux-amd64")

	relProcRunCgoCompile(t, engine, fixture, "linux/amd64")
	relProcAssertHook(t, fixture, true)
	stampPath := filepath.Join(prefix, ".go-mk-cgo-cache-key")
	if _, err := os.Stat(stampPath); !os.IsNotExist(err) {
		t.Fatalf("stamp stat error = %v, want missing stamp", err)
	}
}

// TestGoMkCgoDepsHookProvisionsConsumerTarget runs the real go.mk go-mk-cgo-deps
// target against a hermetic fixture consumer (no network: GO_MK_DEV_DIR plus
// _GO_MK_PROVISIONED), proving the loop runs a consumer go-mk-cgo-dep-<dep> target
// with GO_MK_CGO_PREFIX and PKG_CONFIG_PATH reaching the recipe so a .pc file
// lands under the per-target prefix.
func TestGoMkCgoDepsHookProvisionsConsumerTarget(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not available")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".make"), 0o755); err != nil {
		t.Fatalf("mkdir .make: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".make", "golangci.yml"), nil, 0o644); err != nil {
		t.Fatalf("seed golangci.yml: %v", err)
	}
	makefile := "GO_MK_DEV_DIR := " + repoRoot + "\n" +
		"_GO_MK_PROVISIONED := 1\n" +
		"GO_MK_CGO_DEPS := demolib\n" +
		"include " + filepath.Join(repoRoot, "go.mk") + "\n\n" +
		".PHONY: go-mk-cgo-dep-demolib\n" +
		"go-mk-cgo-dep-demolib:\n" +
		"\t@mkdir -p \"$$GO_MK_CGO_PREFIX/lib/pkgconfig\"\n" +
		"\t@printf 'Name: demolib\\n' > \"$$GO_MK_CGO_PREFIX/lib/pkgconfig/demolib.pc\"\n"
	if err := os.WriteFile(filepath.Join(workDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	cmd := exec.Command(makeBin, "go-mk-cgo-deps",
		"GO_MK_TARGET_GOOS=darwin", "GO_MK_TARGET_GOARCH=arm64", "_GO_MK_PROVISIONED=1")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make go-mk-cgo-deps failed: %v\n%s", err, out)
	}
	provisioned := filepath.Join(workDir, ".make", "cgo", "darwin-arm64", "lib", "pkgconfig", "demolib.pc")
	if _, err := os.Stat(provisioned); err != nil {
		t.Fatalf("expected provisioned .pc at %s: %v", provisioned, err)
	}
}

// TestPublishStageCreatesTagThroughGhReleaseCreate proves the publish stage
// leaves tag creation to `gh release create --target` and never pushes a tag
// with git, for both a prerelease and a stable release.
func TestPublishStageCreatesTagThroughGhReleaseCreate(t *testing.T) {
	const targetSHA = "0123456789abcdef0123456789abcdef01234567"
	testCases := []struct {
		name        string
		tag         string
		prerelease  bool
		channelFlag string
	}{
		{name: "prerelease", tag: "202609151509-7b-0123456", prerelease: true, channelFlag: "--prerelease"},
		{name: "stable", tag: "v1.2.3", prerelease: false, channelFlag: "--latest"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ghLog, gitLog := installFakeReleaseTools(t)
			t.Chdir(t.TempDir())
			distDir := "dist"
			archive := filepath.Join(distDir, "agent-gate_linux_amd64.tar.gz")
			writeFile(t, archive, "archive")

			err := publishStage(releaseConfig{
				binary:     "agent-gate",
				distDir:    distDir,
				tag:        testCase.tag,
				targetSHA:  targetSHA,
				prerelease: testCase.prerelease,
			})
			if err != nil {
				t.Fatalf("publishStage() error = %v, want nil", err)
			}
			wantGhArgs := []string{
				"release", "create", testCase.tag,
				"--target", targetSHA,
				"--title", "agent-gate " + testCase.tag,
				"--generate-notes",
				testCase.channelFlag,
				archive,
				filepath.Join(distDir, "checksums.txt"),
			}
			if gotGhArgs := readFakeToolLog(t, ghLog); !slices.Equal(gotGhArgs, wantGhArgs) {
				t.Fatalf("gh args = %q, want %q", gotGhArgs, wantGhArgs)
			}
			if gitArgs := readFakeToolLog(t, gitLog); slices.Contains(gitArgs, "push") {
				t.Fatalf("publishStage ran git with push, args = %q", gitArgs)
			}
		})
	}
}

// installFakeReleaseTools puts recording gh and git executables first on PATH
// and returns their argument logs. The fake git refuses push, so any tag push
// fails the release instead of reaching a remote.
func installFakeReleaseTools(t *testing.T) (string, string) {
	t.Helper()
	binDir := t.TempDir()
	logDir := t.TempDir()
	ghLog := filepath.Join(logDir, "gh.log")
	gitLog := filepath.Join(logDir, "git.log")
	ghScript := `#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$@" >> "${RELEASE_TEST_GH_LOG}"
`
	gitScript := `#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$@" >> "${RELEASE_TEST_GIT_LOG}"
for arg in "$@"; do
    if [[ "${arg}" == "push" ]]; then
        printf '%s\n' "fake git refuses push" >&2
        exit 1
    fi
done
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(ghScript), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("RELEASE_TEST_GH_LOG", ghLog)
	t.Setenv("RELEASE_TEST_GIT_LOG", gitLog)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return ghLog, gitLog
}

// readFakeToolLog returns the arguments a fake tool recorded, one per line, or
// nil when the tool never ran.
func readFakeToolLog(t *testing.T, logPath string) []string {
	t.Helper()
	content, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", logPath, err)
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

// pkgConfigEntry returns the PKG_CONFIG_PATH entry in env, or "" when absent.
func pkgConfigEntry(env []string) string {
	for _, entry := range env {
		if strings.HasPrefix(entry, "PKG_CONFIG_PATH=") {
			return entry
		}
	}
	return ""
}

type relProcFixture struct {
	workDir    string
	markerPath string
	envPath    string
}

func relProcEnvironment(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("release process fixtures use POSIX shell scripts")
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	for key, value := range map[string]string{
		"HOME": workDir, "XDG_CACHE_HOME": filepath.Join(workDir, "cache"),
		"XDG_CONFIG_HOME": filepath.Join(workDir, "config"), "PATH": "/usr/bin:/bin",
		"GIT_CONFIG_GLOBAL": filepath.Join(workDir, "gitconfig"), "GIT_CONFIG_SYSTEM": "/dev/null",
		"GIT_AUTHOR_NAME": "Release Test", "GIT_AUTHOR_EMAIL": "release@example.test",
		"GIT_COMMITTER_NAME": "Release Test", "GIT_COMMITTER_EMAIL": "release@example.test",
		"MAKEFLAGS": "", "MFLAGS": "", "MAKELEVEL": "", "MAKEFILES": "", "GNUMAKEFLAGS": "",
		"GO_MK_CGO_DEPS": "demolib", "GO_MK_CGO_CACHE_HIT": "", "GO_MK_CGO_CACHE_KEY": "",
		"GO_MK_TARGET_GOOS": "", "GO_MK_TARGET_GOARCH": "", "GO_MK_CGO_PREFIX": "",
		"GO_MK_CC": "", "GO_MK_CXX": "", "CC": "", "CXX": "", "PKG_CONFIG_PATH": "",
		"QUILL_SIGN_P12": "", "QUILL_SIGN_PASSWORD": "", "QUILL_NOTARY_KEY": "",
	} {
		t.Setenv(key, value)
	}
	if err := os.WriteFile(filepath.Join(workDir, "gitconfig"), nil, 0o600); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	// macOS resolves temporary directory symlinks when the process reads its working directory.
	resolvedDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read working directory: %v", err)
	}
	return resolvedDir
}

func relProcCgoFixture(t *testing.T) relProcFixture {
	t.Helper()
	workDir := relProcEnvironment(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Fatalf("find make: %v", err)
	}
	makefile := `.PHONY: go-mk-cgo-deps
go-mk-cgo-deps:
	@printf '%s\n' "GO_MK_TARGET_GOOS=$$GO_MK_TARGET_GOOS" "GO_MK_TARGET_GOARCH=$$GO_MK_TARGET_GOARCH" "GO_MK_CGO_PREFIX=$$GO_MK_CGO_PREFIX" "CC=$$CC" "CXX=$$CXX" > hook.env
	@printf 'hook\n' >> hook.marker
	@if test -z "$$GO_MK_CGO_PREFIX"; then printf 'GO_MK_CGO_PREFIX is unset\n' >&2; exit 1; fi
	@mkdir -p "$$GO_MK_CGO_PREFIX/lib/pkgconfig"
	@printf 'Name: demolib\n' > "$$GO_MK_CGO_PREFIX/lib/pkgconfig/demolib.pc"
`
	if err := os.WriteFile(filepath.Join(workDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	return relProcFixture{
		workDir: workDir, markerPath: filepath.Join(workDir, "hook.marker"),
		envPath: filepath.Join(workDir, "hook.env"),
	}
}

func relProcCgoCommandFixture(t *testing.T) (string, relProcFixture) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("release process fixtures use POSIX shell scripts")
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("find go: %v", err)
	}
	buildDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(buildDir, "gitconfig"), nil, 0o600); err != nil {
		t.Fatalf("write build git config: %v", err)
	}
	for key, value := range map[string]string{
		"HOME": buildDir, "GOCACHE": filepath.Join(buildDir, "cache"),
		"GOPATH": filepath.Join(buildDir, "go"), "GOMODCACHE": filepath.Join(buildDir, "modules"),
		"GOENV": "off", "GOWORK": "off", "GOFLAGS": "-modcacherw", "GOTOOLCHAIN": "local",
		"GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0",
		"GIT_CONFIG_GLOBAL": filepath.Join(buildDir, "gitconfig"), "GIT_CONFIG_SYSTEM": "/dev/null",
		"GIT_AUTHOR_NAME": "Release Test", "GIT_AUTHOR_EMAIL": "release@example.test",
		"GIT_COMMITTER_NAME": "Release Test", "GIT_COMMITTER_EMAIL": "release@example.test",
	} {
		t.Setenv(key, value)
	}
	engine := builtTestEngine(t)
	fixture := relProcCgoFixture(t)
	t.Setenv("PATH", filepath.Dir(goPath)+string(os.PathListSeparator)+"/usr/bin:/bin")
	for name, content := range map[string]string{
		"go.mod":  "module example.test/release\n\ngo 1.26\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(fixture.workDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for _, arguments := range [][]string{
		{"init"}, {"add", "."}, {"-c", "commit.gpgsign=false", "commit", "-m", "Add release fixture"},
	} {
		command := exec.Command("git", arguments...)
		command.Dir = fixture.workDir
		command.Env = relProcCgoCommandEnv(fixture)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
		}
	}
	return engine, fixture
}

func relProcCgoCommandEnv(fixture relProcFixture) []string {
	environment := []string{
		"BINARY=consumer", "CMD=.", "RELEASE_STAGE=compile", "RELEASE_TAG=v1.0.0",
		"HOME=" + fixture.workDir, "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(fixture.workDir, "gitconfig"),
		"GIT_AUTHOR_NAME=Release Test", "GIT_AUTHOR_EMAIL=release@example.test",
		"GIT_COMMITTER_NAME=Release Test", "GIT_COMMITTER_EMAIL=release@example.test",
		"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0",
		"XDG_CACHE_HOME=" + filepath.Join(fixture.workDir, "cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(fixture.workDir, "config"),
	}
	for _, key := range []string{
		"PATH", "GOCACHE", "GOPATH", "GOMODCACHE",
		"GO_MK_CGO_DEPS", "GO_MK_CGO_CACHE_KEY", "GO_MK_CGO_CACHE_HIT",
	} {
		environment = append(environment, key+"="+os.Getenv(key))
	}
	return environment
}

func relProcRunCgoCompile(t *testing.T, engine string, fixture relProcFixture, platform string) {
	t.Helper()
	command := exec.Command(engine, "release")
	command.Dir = fixture.workDir
	command.Env = append(relProcCgoCommandEnv(fixture), "RELEASE_PLATFORMS="+platform)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go-mk release compile: %v\n%s", err, output)
	}
	osName, arch, _ := strings.Cut(platform, "/")
	if _, err := os.Stat(filepath.Join(fixture.workDir, "dist", "consumer_"+osName+"_"+arch, "consumer")); err != nil {
		t.Fatalf("stat compiled consumer: %v", err)
	}
}

func relProcReadFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	return string(content)
}

func relProcAssertHook(t *testing.T, fixture relProcFixture, wantRan bool) {
	t.Helper()
	if !wantRan {
		if _, err := os.Stat(fixture.markerPath); !os.IsNotExist(err) {
			t.Fatalf("hook marker stat error = %v, want missing marker", err)
		}
		return
	}
	if got := relProcReadFile(t, fixture.markerPath); got != "hook\n" {
		t.Fatalf("hook marker = %q, want one hook invocation", got)
	}
}

var (
	relProcQuillOnce   sync.Once
	relProcQuillBinary []byte
	relProcQuillError  error
)

func relProcQuill(t *testing.T) string {
	t.Helper()
	relProcQuillOnce.Do(func() {
		const installSpec = "github.com/anchore/quill/cmd/quill@v0.7.1"
		goPath, err := exec.LookPath("go")
		if err != nil {
			relProcQuillError = fmt.Errorf("find Go toolchain: %w", err)
			return
		}
		installDir := t.TempDir()
		cacheCommand := exec.Command(goPath, "env", "GOMODCACHE", "GOCACHE")
		cacheCommand.Dir = installDir
		cacheCommand.Env = []string{
			"HOME=" + os.Getenv("HOME"), "GOENV=off", "GOWORK=off",
			"PATH=" + filepath.Dir(goPath) + string(os.PathListSeparator) + "/usr/bin:/bin",
		}
		for _, key := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
			if value := os.Getenv(key); value != "" {
				cacheCommand.Env = append(cacheCommand.Env, key+"="+value)
			}
		}
		cacheOutput, err := cacheCommand.CombinedOutput()
		if err != nil {
			relProcQuillError = fmt.Errorf("resolve ambient Go caches: %w\n%s", err, cacheOutput)
			return
		}
		cachePaths := strings.Split(strings.TrimSpace(string(cacheOutput)), "\n")
		if len(cachePaths) != 2 {
			relProcQuillError = fmt.Errorf("resolve ambient Go caches: want two paths, got %q", cacheOutput)
			return
		}
		command := exec.Command(goPath, "install", installSpec)
		command.Dir = installDir
		command.Env = []string{
			"HOME=" + installDir, "GOBIN=" + installDir,
			"GOPATH=" + filepath.Join(installDir, "go"),
			"GOMODCACHE=" + cachePaths[0], "GOCACHE=" + cachePaths[1],
			"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0",
			"GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH,
			"PATH=" + filepath.Dir(goPath) + string(os.PathListSeparator) + "/usr/bin:/bin",
		}
		if output, err := command.CombinedOutput(); err != nil {
			relProcQuillError = fmt.Errorf("go install %s failed; downloading quill is required: %w\n%s", installSpec, err, output)
			return
		}
		relProcQuillBinary, relProcQuillError = os.ReadFile(filepath.Join(installDir, "quill"))
	})
	if relProcQuillError != nil {
		t.Fatalf("install real quill: %v", relProcQuillError)
	}
	// Each invocation copies the installed binary because test cleanup removes temporary directories.
	quillPath := filepath.Join(t.TempDir(), "quill")
	if err := os.WriteFile(quillPath, relProcQuillBinary, 0o755); err != nil {
		t.Fatalf("write installed quill binary: %v", err)
	}
	return quillPath
}

func relProcDisableRetryDelay(t *testing.T) {
	t.Helper()
	originalDelay := darwinSignRetryInterval
	t.Cleanup(func() { darwinSignRetryInterval = originalDelay })
	darwinSignRetryInterval = 0
}

var errStubRetry = sentinelRetryError("retry me")

type sentinelRetryError string

func (e sentinelRetryError) Error() string {
	return string(e)
}
