package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadReleaseConfigParsesPerBinaryOptions(t *testing.T) {
	t.Setenv("BINARY", "mwan")
	t.Setenv("CMD", "./cmd/mwan")
	t.Setenv("RELEASE_BINS", "mwan:./cmd/mwan mwan-wanconfig:./cmd/mwan:cgo=1,platforms=linux/amd64")
	t.Setenv("RELEASE_PLATFORMS", "linux/amd64 freebsd/amd64")

	cfg, err := loadReleaseConfig()
	if err != nil {
		t.Fatalf("loadReleaseConfig() error: %v", err)
	}
	wantBinaries := []releaseBinary{
		{name: "mwan", mainPkg: "./cmd/mwan"},
		{name: "mwan-wanconfig", mainPkg: "./cmd/mwan", cgo: true, platforms: "linux/amd64"},
	}
	if !slices.Equal(cfg.binaries, wantBinaries) {
		t.Fatalf("binaries = %#v, want %#v", cfg.binaries, wantBinaries)
	}
}

func TestLoadReleaseConfigParsesRepeatedPlatformsOption(t *testing.T) {
	t.Setenv("BINARY", "tool")
	t.Setenv("CMD", "./cmd/tool")
	t.Setenv("RELEASE_BINS", "tool:./cmd/tool:platforms=linux/amd64,platforms=linux/arm64")
	t.Setenv("RELEASE_PLATFORMS", "linux/amd64 linux/arm64 darwin/arm64")

	cfg, err := loadReleaseConfig()
	if err != nil {
		t.Fatalf("loadReleaseConfig() error: %v", err)
	}
	if cfg.binaries[0].platforms != "linux/amd64 linux/arm64" {
		t.Fatalf("platforms = %q, want %q", cfg.binaries[0].platforms, "linux/amd64 linux/arm64")
	}
}

func TestLoadReleaseConfigRejectsUnknownBinaryOption(t *testing.T) {
	t.Setenv("BINARY", "tool")
	t.Setenv("CMD", "./cmd/tool")
	t.Setenv("RELEASE_BINS", "tool:./cmd/tool:static=1")

	_, err := loadReleaseConfig()
	if err == nil {
		t.Fatal("loadReleaseConfig() error = nil, want unknown option error")
	}
	if !strings.Contains(err.Error(), `unknown RELEASE_BINS option "static"`) {
		t.Fatalf("loadReleaseConfig() error = %v", err)
	}
}

func TestReleaseBinaryBuildsForHonorsPlatformFilter(t *testing.T) {
	filtered := releaseBinary{name: "mwan-wanconfig", platforms: "linux/amd64"}
	if !filtered.buildsFor("linux/amd64") {
		t.Fatal("buildsFor(linux/amd64) = false, want true for a listed target")
	}
	if filtered.buildsFor("freebsd/amd64") {
		t.Fatal("buildsFor(freebsd/amd64) = true, want false for an unlisted target")
	}
	unfiltered := releaseBinary{name: "mwan"}
	if !unfiltered.buildsFor("freebsd/amd64") {
		t.Fatal("buildsFor(freebsd/amd64) = false, want true when no filter is set")
	}
}

func TestReleaseBinaryBuildEnvForcesCgoWithoutDuplicateKey(t *testing.T) {
	platformEnv := []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"}

	plain := releaseBinary{name: "mwan"}.buildEnv(platformEnv)
	if !slices.Equal(plain, platformEnv) {
		t.Fatalf("buildEnv without cgo = %#v, want the platform environment unchanged", plain)
	}

	forced := releaseBinary{name: "mwan-wanconfig", cgo: true}.buildEnv(platformEnv)
	wantForced := []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1"}
	if !slices.Equal(forced, wantForced) {
		t.Fatalf("buildEnv with cgo = %#v, want %#v", forced, wantForced)
	}
}

func TestArchivePlatformsSkipsBinariesFilteredOffAPlatform(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	distDir := "dist"
	binaries := []releaseBinary{
		{name: "mwan", mainPkg: "./cmd/mwan"},
		{name: "mwan-wanconfig", mainPkg: "./cmd/mwan", cgo: true, platforms: "linux/amd64"},
	}
	platforms := []string{"linux/amd64", "freebsd/amd64"}
	for _, binary := range binaries {
		for _, platform := range platforms {
			if !binary.buildsFor(platform) {
				continue
			}
			osName, arch, _ := strings.Cut(platform, "/")
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
		binary:    "mwan",
		binaries:  binaries,
		platforms: platforms,
		distDir:   distDir,
	})
	if err != nil {
		t.Fatalf("archivePlatforms() error: %v", err)
	}
	wantArchives := []string{
		filepath.Join(distDir, "mwan_linux_amd64.tar.gz"),
		filepath.Join(distDir, "mwan-wanconfig_linux_amd64.tar.gz"),
		filepath.Join(distDir, "mwan_freebsd_amd64.tar.gz"),
	}
	if !slices.Equal(archives, wantArchives) {
		t.Fatalf("archives = %#v, want %#v", archives, wantArchives)
	}
}
