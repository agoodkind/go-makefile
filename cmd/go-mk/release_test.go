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
