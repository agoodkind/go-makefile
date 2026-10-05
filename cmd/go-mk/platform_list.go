package main

import (
	"fmt"
	"os"
	"strings"
)

// declaredPlatforms returns the os/arch targets the module ships: the
// `GO_MK_PLATFORMS` list, or `defaultReleasePlatforms` when that list is empty.
func declaredPlatforms() []string {
	declared := platformMatrix()
	if len(declared) == 0 {
		return strings.Fields(defaultReleasePlatforms)
	}
	platforms := make([]string, 0, len(declared))
	for _, target := range declared {
		platforms = append(platforms, target.label())
	}
	return platforms
}

// The reusable workflows read `platforms=<list>` from `GITHUB_OUTPUT` to plan
// the compile and package matrices.
func runPlatforms() int {
	list := strings.Join(declaredPlatforms(), " ")
	writeStdout(list + "\n")
	outputPath := os.Getenv("GITHUB_OUTPUT")
	if outputPath == "" {
		return 0
	}
	output, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		writeStderr("go-mk platforms: open GITHUB_OUTPUT: " + err.Error() + "\n")
		return 1
	}
	defer func() {
		_ = output.Close()
	}()
	if _, err := fmt.Fprintf(output, "platforms=%s\n", list); err != nil {
		writeStderr("go-mk platforms: write GITHUB_OUTPUT: " + err.Error() + "\n")
		return 1
	}
	return 0
}
