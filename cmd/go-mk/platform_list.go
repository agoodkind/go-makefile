package main

import (
	"os"
	"strings"
)

// declaredPlatforms returns the os/arch targets the module ships: the
// `GO_MK_PLATFORMS` list, or `GO_MK_DEFAULT_PLATFORMS` from go.mk when that list
// is empty. `defaultReleasePlatforms` covers a run outside make.
func declaredPlatforms() []string {
	declared := platformMatrix()
	if len(declared) == 0 {
		if fromMake := strings.Fields(os.Getenv("GO_MK_DEFAULT_PLATFORMS")); len(fromMake) > 0 {
			return fromMake
		}
		return strings.Fields(defaultReleasePlatforms)
	}
	platforms := make([]string, 0, len(declared))
	for _, target := range declared {
		platforms = append(platforms, target.label())
	}
	return platforms
}
