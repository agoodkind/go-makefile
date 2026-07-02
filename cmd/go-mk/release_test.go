package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		return errStubRetry
	}
	releaseSleep = func(_ time.Duration) {}
	darwinSignAttempts = 2
	darwinSignRetryInterval = 0

	err := signAndNotarizeDarwinBinary("quill", "dist/agent-gate", "")
	if err != errStubRetry {
		t.Fatalf("signAndNotarizeDarwinBinary() error = %v, want %v", err, errStubRetry)
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want 2", callCount)
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

// TestProvisionCgoDepsNoOpWhenUnset proves an empty GO_MK_CGO_DEPS starts no
// process and returns an empty path, the hard constraint that keeps a pure-Go
// release byte-identical.
func TestProvisionCgoDepsNoOpWhenUnset(t *testing.T) {
	t.Setenv("GO_MK_CGO_DEPS", "")
	originalRunProcess := releaseRunProcess
	t.Cleanup(func() { releaseRunProcess = originalRunProcess })
	called := false
	releaseRunProcess = func(_ string, _ []string, _ []string) error {
		called = true
		return nil
	}
	dir, err := provisionCgoDeps("linux", "amd64")
	if err != nil {
		t.Fatalf("provisionCgoDeps error = %v, want nil", err)
	}
	if dir != "" {
		t.Fatalf("provisionCgoDeps dir = %q, want empty", dir)
	}
	if called {
		t.Fatal("provisionCgoDeps ran a process for an empty GO_MK_CGO_DEPS, want no-op")
	}
}

// TestProvisionCgoDepsRunsHookAndComposesEnv proves a declared GO_MK_CGO_DEPS
// invokes the make hook with the per-target os/arch and prefix, and returns the
// pkg-config directory under that prefix.
func TestProvisionCgoDepsRunsHookAndComposesEnv(t *testing.T) {
	t.Setenv("GO_MK_CGO_DEPS", "demolib")
	originalRunProcess := releaseRunProcess
	t.Cleanup(func() { releaseRunProcess = originalRunProcess })
	var gotName string
	var gotArgs, gotEnv []string
	releaseRunProcess = func(name string, args []string, env []string) error {
		gotName, gotArgs, gotEnv = name, args, env
		return nil
	}
	dir, err := provisionCgoDeps("darwin", "arm64")
	if err != nil {
		t.Fatalf("provisionCgoDeps error = %v", err)
	}
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	wantPrefix := cgoPrefixForTarget(workDir, "darwin", "arm64")
	wantDir := filepath.Join(wantPrefix, "lib", "pkgconfig")
	if dir != wantDir {
		t.Fatalf("provisionCgoDeps dir = %q, want %q", dir, wantDir)
	}
	if gotName != "make" {
		t.Fatalf("ran %q, want make", gotName)
	}
	if len(gotArgs) != 1 || gotArgs[0] != cgoDepsTarget {
		t.Fatalf("args = %v, want [%s]", gotArgs, cgoDepsTarget)
	}
	for _, want := range []string{
		"GO_MK_TARGET_GOOS=darwin",
		"GO_MK_TARGET_GOARCH=arm64",
		"GO_MK_CGO_PREFIX=" + wantPrefix,
	} {
		if !envContains(gotEnv, want) {
			t.Fatalf("hook env missing %q: %v", want, gotEnv)
		}
	}
}

func TestProvisionCgoDepsSkipsWarmCache(t *testing.T) {
	t.Setenv("GO_MK_CGO_DEPS", "demolib")
	t.Setenv("GO_MK_CGO_CACHE_HIT", "true")
	t.Setenv("GO_MK_CGO_CACHE_KEY", "cache-key-1")
	t.Chdir(t.TempDir())

	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	prefix := cgoPrefixForTarget(workDir, "darwin", "arm64")
	pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")
	if err := os.MkdirAll(pkgConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg-config dir: %v", err)
	}
	stampPath := filepath.Join(prefix, ".go-mk-cgo-cache-key")
	if err := os.WriteFile(stampPath, []byte("cache-key-1"), 0o644); err != nil {
		t.Fatalf("write stamp: %v", err)
	}

	originalRunProcess := releaseRunProcess
	t.Cleanup(func() { releaseRunProcess = originalRunProcess })
	called := false
	releaseRunProcess = func(_ string, _ []string, _ []string) error {
		called = true
		return nil
	}

	dir, err := provisionCgoDeps("darwin", "arm64")
	if err != nil {
		t.Fatalf("provisionCgoDeps error = %v, want nil", err)
	}
	if dir != pkgConfigDir {
		t.Fatalf("provisionCgoDeps dir = %q, want %q", dir, pkgConfigDir)
	}
	if called {
		t.Fatal("provisionCgoDeps ran make on a warm cgo cache, want skip")
	}
}

func TestProvisionCgoDepsSkipsWarmCacheWithTrailingNewlineStamp(t *testing.T) {
	t.Setenv("GO_MK_CGO_DEPS", "demolib")
	t.Setenv("GO_MK_CGO_CACHE_HIT", "true")
	t.Setenv("GO_MK_CGO_CACHE_KEY", "cache-key-1")
	t.Chdir(t.TempDir())

	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	prefix := cgoPrefixForTarget(workDir, "darwin", "arm64")
	pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")
	if err := os.MkdirAll(pkgConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg-config dir: %v", err)
	}
	stampPath := filepath.Join(prefix, ".go-mk-cgo-cache-key")
	if err := os.WriteFile(stampPath, []byte("cache-key-1\n"), 0o644); err != nil {
		t.Fatalf("write stamp: %v", err)
	}

	originalRunProcess := releaseRunProcess
	t.Cleanup(func() { releaseRunProcess = originalRunProcess })
	called := false
	releaseRunProcess = func(_ string, _ []string, _ []string) error {
		called = true
		return nil
	}

	if _, err := provisionCgoDeps("darwin", "arm64"); err != nil {
		t.Fatalf("provisionCgoDeps error = %v, want nil", err)
	}
	if called {
		t.Fatal("provisionCgoDeps rebuilt on a stamp that only differs by a trailing newline, want skip")
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
			t.Setenv("GO_MK_CGO_DEPS", "demolib")
			t.Setenv("GO_MK_CGO_CACHE_HIT", testCase.cacheHit)
			t.Setenv("GO_MK_CGO_CACHE_KEY", testCase.cacheKey)
			t.Chdir(t.TempDir())

			workDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("Getwd error = %v", err)
			}
			prefix := cgoPrefixForTarget(workDir, "darwin", "arm64")
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

			originalRunProcess := releaseRunProcess
			t.Cleanup(func() { releaseRunProcess = originalRunProcess })
			callCount := 0
			releaseRunProcess = func(_ string, _ []string, _ []string) error {
				callCount++
				if err := os.MkdirAll(pkgConfigDir, 0o755); err != nil {
					t.Fatalf("mkdir pkg-config dir in hook: %v", err)
				}
				return nil
			}

			dir, err := provisionCgoDeps("darwin", "arm64")
			if err != nil {
				t.Fatalf("provisionCgoDeps error = %v, want nil", err)
			}
			if dir != pkgConfigDir {
				t.Fatalf("provisionCgoDeps dir = %q, want %q", dir, pkgConfigDir)
			}
			if callCount != 1 {
				t.Fatalf("releaseRunProcess call count = %d, want 1", callCount)
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
	t.Setenv("GO_MK_CGO_DEPS", "demolib")
	t.Setenv("GO_MK_CGO_CACHE_KEY", "cache-key-1")
	t.Chdir(t.TempDir())

	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	prefix := cgoPrefixForTarget(workDir, "linux", "amd64")
	pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")

	originalRunProcess := releaseRunProcess
	t.Cleanup(func() { releaseRunProcess = originalRunProcess })
	releaseRunProcess = func(_ string, _ []string, _ []string) error {
		return os.MkdirAll(pkgConfigDir, 0o755)
	}

	if _, err := provisionCgoDeps("linux", "amd64"); err != nil {
		t.Fatalf("provisionCgoDeps error = %v, want nil", err)
	}
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
	t.Setenv("GO_MK_CGO_DEPS", "demolib")
	t.Setenv("GO_MK_CGO_CACHE_KEY", "")
	t.Chdir(t.TempDir())

	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	prefix := cgoPrefixForTarget(workDir, "linux", "amd64")
	pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")

	originalRunProcess := releaseRunProcess
	t.Cleanup(func() { releaseRunProcess = originalRunProcess })
	releaseRunProcess = func(_ string, _ []string, _ []string) error {
		return os.MkdirAll(pkgConfigDir, 0o755)
	}

	if _, err := provisionCgoDeps("linux", "amd64"); err != nil {
		t.Fatalf("provisionCgoDeps error = %v, want nil", err)
	}
	stampPath := filepath.Join(prefix, ".go-mk-cgo-cache-key")
	if _, err := os.Stat(stampPath); !os.IsNotExist(err) {
		t.Fatalf("stamp stat error = %v, want missing stamp", err)
	}
}

// TestGoMkCgoDepsHookProvisionsConsumerTarget runs the real go.mk go-mk-cgo-deps
// target against a hermetic fixture consumer (no network: GO_MK_DEV_DIR plus
// GO_MK_SKIP_FETCH), proving the loop runs a consumer go-mk-cgo-dep-<dep> target
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
		"GO_MK_SKIP_FETCH := 1\n" +
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
		"GO_MK_TARGET_GOOS=darwin", "GO_MK_TARGET_GOARCH=arm64", "GO_MK_SKIP_FETCH=1")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make go-mk-cgo-deps failed: %v\n%s", err, out)
	}
	provisioned := filepath.Join(workDir, ".make", "cgo", "darwin-arm64", "lib", "pkgconfig", "demolib.pc")
	if _, err := os.Stat(provisioned); err != nil {
		t.Fatalf("expected provisioned .pc at %s: %v", provisioned, err)
	}
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

var errStubRetry = sentinelRetryError("retry me")

type sentinelRetryError string

func (e sentinelRetryError) Error() string {
	return string(e)
}
