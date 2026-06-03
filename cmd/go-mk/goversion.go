// go version currency check for go-mk. It compares the module's pinned Go
// version against the latest stable Go release and surfaces a loud, actionable
// upgrade instruction during build-check. The project policy is to track the
// latest Go, so the notice tells the reader exactly which go.mod directives to
// bump rather than leaving an old toolchain in place. The check is best-effort:
// it caches the latest version for a day and stays silent when offline.
package main

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// goVersionEndpoint returns the latest stable Go version as a single line like
// "go1.26.4" followed by build metadata.
const goVersionEndpoint = "https://go.dev/VERSION?m=text"

// goLatestCacheTTL bounds how long a fetched latest-version answer is reused so
// repeated local builds do not hit the network each time.
const goLatestCacheTTL = 24 * time.Hour

// goVersionNotice returns an actionable upgrade message when the module's
// effective Go version is behind the latest stable Go, or an empty string when
// it is current or the check cannot run.
func goVersionNotice() string {
	moduleVersion, ok := moduleGoVersion()
	if !ok {
		return ""
	}
	latest, ok := latestStableGo()
	if !ok {
		return ""
	}
	if compareGoVersions(moduleVersion, latest) >= 0 {
		return ""
	}
	return "go-version: Go " + moduleVersion + " is behind the latest stable Go " + latest + ".\n" +
		"Project policy tracks the latest Go. Update go.mod:\n" +
		"    go " + latest + "\n" +
		"    toolchain go" + latest + "\n" +
		"then run `make build` again."
}

// applyGoVersionNotice prints the upgrade notice when the module is behind and
// returns the build status, escalating to failure only when
// GO_MK_REQUIRE_LATEST_GO is truthy so the default behaviour is informative.
func applyGoVersionNotice(status int) int {
	notice := goVersionNotice()
	if notice == "" {
		return status
	}
	slog.Warn("go version behind latest stable")
	writeStdout("\n  " + strings.ReplaceAll(notice, "\n", "\n  ") + "\n")
	if status == 0 && isTruthy(os.Getenv("GO_MK_REQUIRE_LATEST_GO")) {
		return 1
	}
	return status
}

// runGoVersionCheck prints the upgrade notice for the current module and is the
// standalone entry point behind `go-mk go-version-check`. It returns non-zero
// only when GO_MK_REQUIRE_LATEST_GO is truthy and the module is behind.
func runGoVersionCheck() int {
	notice := goVersionNotice()
	if notice == "" {
		writeStdout("go-version: up to date\n")
		return 0
	}
	slog.Warn("go version behind latest stable")
	writeStdout(notice + "\n")
	if isTruthy(os.Getenv("GO_MK_REQUIRE_LATEST_GO")) {
		return 1
	}
	return 0
}

// moduleGoVersion returns the module's effective Go version, preferring the
// toolchain directive over the go directive, both read from go.mod.
func moduleGoVersion() (string, bool) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", false
	}
	goDirective := ""
	toolchainDirective := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == "go" {
			goDirective = fields[1]
		} else if fields[0] == "toolchain" {
			toolchainDirective = strings.TrimPrefix(fields[1], "go")
		}
	}
	if toolchainDirective != "" {
		return toolchainDirective, true
	}
	if goDirective != "" {
		return goDirective, true
	}
	return "", false
}

// latestStableGo returns the latest stable Go version (without the "go"
// prefix), using a day-long cache under .make and falling back to a network
// fetch. It returns ok=false when neither source yields a value.
func latestStableGo() (string, bool) {
	cachePath := filepath.Join(makeDir, "go-latest-version")
	if cached, ok := freshCachedGoVersion(cachePath); ok {
		return cached, true
	}
	version, ok := fetchLatestStableGo()
	if !ok {
		return "", false
	}
	writeGoVersionCache(cachePath, version)
	return version, true
}

// freshCachedGoVersion returns the cached latest version when the cache file is
// younger than the TTL.
func freshCachedGoVersion(cachePath string) (string, bool) {
	info, err := os.Stat(cachePath)
	if err != nil {
		return "", false
	}
	if time.Since(info.ModTime()) > goLatestCacheTTL {
		return "", false
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return "", false
	}
	version := strings.TrimSpace(string(data))
	if version == "" {
		return "", false
	}
	return version, true
}

// writeGoVersionCache stores the latest version under .make, ignoring write
// errors because the check is best-effort.
func writeGoVersionCache(cachePath, version string) {
	slog.Info("go version cache write", slog.String("path", cachePath))
	if err := os.MkdirAll(makeDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(cachePath, []byte(version+"\n"), 0o644)
}

// fetchLatestStableGo queries the Go release endpoint and returns the version
// without the "go" prefix. It is a network boundary, so it emits a slog event.
func fetchLatestStableGo() (string, bool) {
	slog.Info("go version fetch latest", slog.String("endpoint", goVersionEndpoint))
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(goVersionEndpoint)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", false
	}
	firstLine, _, _ := strings.Cut(string(body), "\n")
	version := strings.TrimPrefix(strings.TrimSpace(firstLine), "go")
	if version == "" {
		return "", false
	}
	return version, true
}

// compareGoVersions compares two dotted numeric Go versions, returning -1, 0,
// or 1. Missing trailing components are treated as zero, so "1.26" sorts before
// "1.26.4".
func compareGoVersions(left, right string) int {
	leftParts := goVersionParts(left)
	rightParts := goVersionParts(right)
	count := len(leftParts)
	if len(rightParts) > count {
		count = len(rightParts)
	}
	for index := 0; index < count; index++ {
		leftValue := goVersionComponent(leftParts, index)
		rightValue := goVersionComponent(rightParts, index)
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

// goVersionParts splits a dotted version into its numeric components, stopping
// at the first non-numeric fragment such as a release-candidate suffix.
func goVersionParts(version string) []int {
	parts := []int{}
	for _, fragment := range strings.Split(version, ".") {
		value, err := strconv.Atoi(fragment)
		if err != nil {
			break
		}
		parts = append(parts, value)
	}
	return parts
}

// goVersionComponent returns the component at index, or zero when the version
// has fewer components.
func goVersionComponent(parts []int, index int) int {
	if index < len(parts) {
		return parts[index]
	}
	return 0
}
