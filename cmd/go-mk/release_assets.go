package main

import (
	"fmt"
	"log/slog"
	"strings"
)

// Archive names use RELEASE_BINS and the platform rules from make release.
func runReleaseAssets() int {
	cfg, err := loadReleaseConfig()
	if err != nil {
		slog.Error("release assets config failed", slog.Any("err", err))
		writeStderr("go-mk release-assets: " + err.Error() + "\n")
		return 1
	}
	writeStdout(releaseAssetNames(cfg))
	return 0
}

func releaseAssetNames(cfg releaseConfig) string {
	var lines strings.Builder
	for _, binary := range releaseBinaries(cfg) {
		for _, platform := range cfg.platforms {
			if !binary.buildsFor(platform) {
				continue
			}
			osName, arch, ok := strings.Cut(platform, "/")
			if !ok {
				continue
			}
			fmt.Fprintf(&lines, "%s_%s_%s.tar.gz\n", binary.name, osName, arch)
		}
	}
	return lines.String()
}
