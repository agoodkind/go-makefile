// go version currency check for go-mk. It compares the module's pinned Go
// version against the latest stable Go release and surfaces a loud, actionable
// upgrade instruction during build-check. The project policy is to track the
// latest Go, so the notice tells the reader exactly which go.mod directives to
// bump rather than leaving an old toolchain in place. The check is advisory:
// it caches the latest version for a day and reports lookup failures visibly.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goodkind.io/go-makefile/internal/report"
)

// goVersionEndpoint returns the latest stable Go version as a single line like
// "go1.26.4" followed by build metadata.
const goVersionEndpoint = "https://go.dev/VERSION?m=text"

// goLatestCacheTTL bounds how long a fetched latest-version answer is reused so
// repeated local builds do not hit the network each time.
const goLatestCacheTTL = 24 * time.Hour

type goVersionConfig struct {
	moduleVersion func() (string, error)
	latestVersion func() (string, error)
}

func runGoVersionStep() (report.StepResult, int) {
	return goVersionStepWith(goVersionConfig{
		moduleVersion: moduleGoVersion,
		latestVersion: latestStableGo,
	}), 0
}

func goVersionStepWith(config goVersionConfig) report.StepResult {
	moduleVersion, err := config.moduleVersion()
	if err != nil {
		return advisoryToolFailure("go-version", err.Error())
	}
	latestVersion, err := config.latestVersion()
	if err != nil {
		return advisoryToolFailure("go-version", err.Error())
	}
	if compareGoVersions(moduleVersion, latestVersion) >= 0 {
		return report.StepResult{Name: "go-version", Status: report.StatusOK}
	}
	slog.Warn("go version behind latest stable")
	findings := []string{
		"Go " + moduleVersion + " is behind the latest stable Go " + latestVersion + ".",
		"Project policy tracks the latest Go. Update go.mod:",
		"    go " + latestVersion,
		"    toolchain go" + latestVersion,
		"then run `make build` again.",
	}
	return report.StepResult{
		Name:     "go-version",
		Status:   report.StatusAdvisory,
		Findings: findings,
	}
}

func runGoVersionCheck() int {
	result, _ := runGoVersionStep()
	writeStdout(report.Render(report.Report{
		Title: "go-mk go-version-check",
		Steps: []report.StepResult{result},
	}))
	return 0
}

// moduleGoVersion returns the module's effective Go version, preferring the
// toolchain directive over the go directive, both read from go.mod.
func moduleGoVersion() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		slog.Warn("go version read module failed", slog.Any("err", err))
		return "", fmt.Errorf("read Go version from go.mod: %w", err)
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
		return toolchainDirective, nil
	}
	if goDirective != "" {
		return goDirective, nil
	}
	return "", fmt.Errorf("read Go version from go.mod: no go or toolchain directive")
}

// latestStableGo returns the latest stable Go version (without the "go"
// prefix), using a day-long cache under .make and falling back to a network
// fetch. It returns the network or response failure when no cache is usable.
func latestStableGo() (string, error) {
	cachePath := filepath.Join(makeDir, "go-latest-version")
	if cached, ok := freshCachedGoVersion(cachePath); ok {
		return cached, nil
	}
	version, err := fetchLatestStableGo()
	if err != nil {
		return "", err
	}
	writeGoVersionCache(cachePath, version)
	return version, nil
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
func fetchLatestStableGo() (string, error) {
	slog.Info("go version fetch latest", slog.String("endpoint", goVersionEndpoint))
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(goVersionEndpoint)
	if err != nil {
		slog.Warn("go version lookup request failed", slog.Any("err", err))
		return "", fmt.Errorf("look up latest Go version: network request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("look up latest Go version: server returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		slog.Warn("go version lookup response failed", slog.Any("err", err))
		return "", fmt.Errorf("look up latest Go version: read response: %w", err)
	}
	firstLine, _, _ := strings.Cut(string(body), "\n")
	version := strings.TrimPrefix(strings.TrimSpace(firstLine), "go")
	if version == "" {
		return "", fmt.Errorf("look up latest Go version: server returned no version")
	}
	return version, nil
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
