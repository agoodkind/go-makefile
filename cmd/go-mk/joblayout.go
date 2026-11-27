package main

import (
	"goodkind.io/go-makefile/internal/cigate"
	"goodkind.io/go-makefile/internal/clock"
)

func runCIJobLayout() int {
	return cigate.Run(runCacheManifest, cacheManifestRepoPrefix, defaultLintGates, clock.Now)
}
