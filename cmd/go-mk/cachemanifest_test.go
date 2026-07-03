package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheManifestSkipsTrackedGeneratedOutputs(t *testing.T) {
	repoDir := cacheManifestTestRepo(t)
	mustMkdirAll(t, filepath.Join(repoDir, "gen"))
	writeBootstrapTestFile(t, filepath.Join(repoDir, "gen", "checked.txt"), "tracked\n")
	writeBootstrapTestFile(t, filepath.Join(repoDir, "Makefile"), "all:\n\t@true\n")
	cacheManifestGit(t, repoDir, "add", "gen/checked.txt", "Makefile")
	t.Chdir(repoDir)

	result := runCacheManifestForTest(t, map[string]string{
		"GO_MK_GENERATE":         "generate",
		"GO_MK_GENERATE_INPUTS":  ".",
		"GO_MK_GENERATE_OUTPUTS": "gen/checked.txt cache/out",
	})

	if result.status != 0 {
		t.Fatalf("status = %d, want 0", result.status)
	}
	if result.outputs["generated_cache_enabled"] != "true" {
		t.Fatalf("generated_cache_enabled = %q, want true", result.outputs["generated_cache_enabled"])
	}
	if result.outputs["generated_cache_paths"] != "cache/out" {
		t.Fatalf("generated_cache_paths = %q, want cache/out", result.outputs["generated_cache_paths"])
	}
	if result.outputs["generated_cache_warnings"] != "tracked output skipped: gen/checked.txt" {
		t.Fatalf("generated_cache_warnings = %q, want tracked output warning", result.outputs["generated_cache_warnings"])
	}
	if !strings.Contains(result.stderr, "go-mk-cache-manifest: skip tracked generated output: gen/checked.txt\n") {
		t.Fatalf("stderr missing tracked-output diagnostic:\n%s", result.stderr)
	}
	if !strings.Contains(result.stderr, "tracked output skipped: gen/checked.txt\n") {
		t.Fatalf("stderr missing final warning stream:\n%s", result.stderr)
	}
}

func TestCacheManifestEnabledTruthTable(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		enabled string
	}{
		{
			name: "disabled without generate target",
			env: map[string]string{
				"GO_MK_GENERATE_INPUTS":  "schema",
				"GO_MK_GENERATE_OUTPUTS": "cache/out",
			},
			enabled: "false",
		},
		{
			name: "disabled without inputs",
			env: map[string]string{
				"GO_MK_GENERATE":         "generate",
				"GO_MK_GENERATE_OUTPUTS": "cache/out",
			},
			enabled: "false",
		},
		{
			name: "disabled without outputs",
			env: map[string]string{
				"GO_MK_GENERATE":        "generate",
				"GO_MK_GENERATE_INPUTS": "schema",
			},
			enabled: "false",
		},
		{
			name: "enabled with generate inputs and untracked outputs",
			env: map[string]string{
				"GO_MK_GENERATE":         "generate",
				"GO_MK_GENERATE_INPUTS":  "schema",
				"GO_MK_GENERATE_OUTPUTS": "cache/out",
			},
			enabled: "true",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repoDir := cacheManifestTestRepo(t)
			t.Chdir(repoDir)

			result := runCacheManifestForTest(t, testCase.env)

			if result.outputs["generated_cache_enabled"] != testCase.enabled {
				t.Fatalf("generated_cache_enabled = %q, want %q", result.outputs["generated_cache_enabled"], testCase.enabled)
			}
		})
	}
}

func TestCacheManifestRequiresSubmodules(t *testing.T) {
	repoDir := cacheManifestTestRepo(t)
	writeBootstrapTestFile(t, filepath.Join(repoDir, ".gitmodules"), "[submodule \"vendor/lib\"]\n\tpath = vendor/lib\n\turl = https://example.invalid/lib.git\n")
	cacheManifestGit(t, repoDir, "add", ".gitmodules")
	t.Chdir(repoDir)

	result := runCacheManifestForTest(t, map[string]string{
		"GO_MK_GENERATE":         "generate",
		"GO_MK_GENERATE_INPUTS":  "schema",
		"GO_MK_GENERATE_OUTPUTS": "cache/out",
	})

	if result.outputs["generated_cache_requires_submodules"] != "true" {
		t.Fatalf("generated_cache_requires_submodules = %q, want true", result.outputs["generated_cache_requires_submodules"])
	}
}

func TestCacheManifestWritesGitHubOutputHeredocs(t *testing.T) {
	repoDir := cacheManifestTestRepo(t)
	t.Chdir(repoDir)

	result := runCacheManifestForTest(t, map[string]string{
		"GO_MK_GENERATE":         "generate",
		"GO_MK_GENERATE_INPUTS":  "schema",
		"GO_MK_GENERATE_OUTPUTS": "cache/out",
	})

	expectedNames := []string{
		"generated_cache_enabled",
		"generated_cache_requires_submodules",
		"generated_cache_paths",
		"generated_cache_key",
		"generated_cache_warnings",
		"cgo_cache_enabled",
		"cgo_cache_paths",
		"cgo_cache_key",
	}
	actualNames := githubOutputNames(t, result.githubOutput)
	if strings.Join(actualNames, ",") != strings.Join(expectedNames, ",") {
		t.Fatalf("output names = %v, want %v\nGITHUB_OUTPUT:\n%s", actualNames, expectedNames, result.githubOutput)
	}
	for _, name := range expectedNames {
		header := name + "<<__GO_MK_CACHE_OUTPUT__\n"
		if !strings.Contains(result.githubOutput, header) {
			t.Fatalf("GITHUB_OUTPUT missing heredoc header %q:\n%s", header, result.githubOutput)
		}
	}
	expectedStdout := "go-mk-cache-manifest: generated_cache_enabled=true\n" +
		"go-mk-cache-manifest: generated_cache_requires_submodules=false\n" +
		"go-mk-cache-manifest: generated output paths:\n" +
		"cache/out\n"
	if result.stdout != expectedStdout {
		t.Fatalf("stdout mismatch\nwant:\n%s\ngot:\n%s", expectedStdout, result.stdout)
	}
}

func TestCacheManifestCgoKeyStableAndChangesWithInputFile(t *testing.T) {
	repoDir := cacheManifestTestRepo(t)
	writeBootstrapTestFile(t, filepath.Join(repoDir, "Makefile"), "all:\n\t@true\n")
	mustMkdirAll(t, filepath.Join(repoDir, "scripts"))
	inputPath := filepath.Join(repoDir, "scripts", "build-pcre2.sh")
	writeBootstrapTestFile(t, inputPath, "printf 'pcre2 10.45\\n'\n")
	fakeCompiler := writeFakeCacheManifestCompiler(t, repoDir)
	cacheManifestGit(t, repoDir, "add", "Makefile")
	t.Chdir(repoDir)

	env := map[string]string{
		"GO_MK_CGO_DEPS":           "pcre2",
		"GO_MK_CGO_CACHE_VERSIONS": "pcre2=10.45",
		"GO_MK_CGO_CACHE_INPUTS":   "scripts/build-pcre2.sh",
		"GO_MK_CGO_TOOLCHAIN_ID":   "ghcr.io/goreleaser/goreleaser-cross:v1.26.3",
		"GO_MK_TARGET_GOOS":        "darwin",
		"GO_MK_TARGET_GOARCH":      "arm64",
		"GO_MK_CC":                 fakeCompiler,
	}
	first := runCacheManifestForTest(t, env)
	second := runCacheManifestForTest(t, env)

	if first.outputs["cgo_cache_enabled"] != "true" {
		t.Fatalf("cgo_cache_enabled = %q, want true", first.outputs["cgo_cache_enabled"])
	}
	if first.outputs["cgo_cache_paths"] != ".make/cgo/darwin-arm64" {
		t.Fatalf("cgo_cache_paths = %q, want .make/cgo/darwin-arm64", first.outputs["cgo_cache_paths"])
	}
	if first.outputs["cgo_cache_key"] == "" {
		t.Fatal("cgo_cache_key is empty")
	}
	if first.outputs["cgo_cache_key"] != second.outputs["cgo_cache_key"] {
		t.Fatalf("cgo key changed across stable runs\nfirst: %s\nsecond: %s", first.outputs["cgo_cache_key"], second.outputs["cgo_cache_key"])
	}

	writeBootstrapTestFile(t, inputPath, "printf 'pcre2 10.46\\n'\n")
	after := runCacheManifestForTest(t, env)
	if first.outputs["cgo_cache_key"] == after.outputs["cgo_cache_key"] {
		t.Fatalf("cgo key did not change after cache input changed: %s", first.outputs["cgo_cache_key"])
	}
}

func TestCacheManifestKeyStable(t *testing.T) {
	repoDir := cacheManifestTestRepo(t)
	writeBootstrapTestFile(t, filepath.Join(repoDir, "Makefile"), "all:\n\t@true\n")
	cacheManifestGit(t, repoDir, "add", "Makefile")
	t.Chdir(repoDir)

	env := map[string]string{
		"GO_MK_GENERATE":         "generate",
		"GO_MK_GENERATE_INPUTS":  ".",
		"GO_MK_GENERATE_OUTPUTS": "cache/out",
	}
	first := runCacheManifestForTest(t, env)
	second := runCacheManifestForTest(t, env)

	if first.outputs["generated_cache_key"] == "" {
		t.Fatal("generated_cache_key is empty")
	}
	if first.outputs["generated_cache_key"] != second.outputs["generated_cache_key"] {
		t.Fatalf("cache key changed across stable runs\nfirst: %s\nsecond: %s", first.outputs["generated_cache_key"], second.outputs["generated_cache_key"])
	}
}

func TestCacheManifestKeyChangesWhenTrackedInputChanges(t *testing.T) {
	repoDir := cacheManifestTestRepo(t)
	makefilePath := filepath.Join(repoDir, "Makefile")
	writeBootstrapTestFile(t, makefilePath, "all:\n\t@true\n")
	cacheManifestGit(t, repoDir, "add", "Makefile")
	t.Chdir(repoDir)

	env := map[string]string{
		"GO_MK_GENERATE":         "generate",
		"GO_MK_GENERATE_INPUTS":  ".",
		"GO_MK_GENERATE_OUTPUTS": "cache/out",
	}
	before := runCacheManifestForTest(t, env)
	writeBootstrapTestFile(t, makefilePath, "all:\n\t@printf changed\\n\n")
	after := runCacheManifestForTest(t, env)

	if before.outputs["generated_cache_key"] == after.outputs["generated_cache_key"] {
		t.Fatalf("cache key did not change after tracked input changed: %s", before.outputs["generated_cache_key"])
	}
}

func TestRunningExecutableHashUsesStableLabel(t *testing.T) {
	fileHash, err := runningExecutableHash()
	if err != nil {
		t.Fatalf("runningExecutableHash() error: %v", err)
	}
	if fileHash.path != executableManifestLabel {
		t.Fatalf("executable hash path = %q, want stable label %q", fileHash.path, executableManifestLabel)
	}
	if len(fileHash.digest) != 64 {
		t.Fatalf("executable hash digest = %q, want a 64-char sha256 of the real bytes", fileHash.digest)
	}
}

func TestCacheManifestOutputDelimiterKeepsStableWhenNoCollision(t *testing.T) {
	values := map[string]string{
		"generated_cache_key":      "deadbeef",
		"generated_cache_warnings": "tracked output skipped: gen/out",
	}
	delimiter, err := cacheManifestOutputDelimiterFor(values)
	if err != nil {
		t.Fatalf("cacheManifestOutputDelimiterFor() error: %v", err)
	}
	if delimiter != cacheManifestOutputDelimiter {
		t.Fatalf("delimiter = %q, want stable %q with no collision", delimiter, cacheManifestOutputDelimiter)
	}
}

func TestCacheManifestOutputDelimiterExtendsOnCollision(t *testing.T) {
	values := map[string]string{
		"generated_cache_warnings": "safe line\n" + cacheManifestOutputDelimiter + "\nmore",
	}
	delimiter, err := cacheManifestOutputDelimiterFor(values)
	if err != nil {
		t.Fatalf("cacheManifestOutputDelimiterFor() error: %v", err)
	}
	if delimiter == cacheManifestOutputDelimiter {
		t.Fatal("delimiter did not extend past a value containing the stable delimiter line")
	}
	if !strings.HasPrefix(delimiter, cacheManifestOutputDelimiter+"-") {
		t.Fatalf("extended delimiter = %q, want %q- prefix", delimiter, cacheManifestOutputDelimiter)
	}
	if cacheManifestValuesContainDelimiter(values, delimiter) {
		t.Fatal("extended delimiter still collides with a value line")
	}
}

func TestCacheManifestHashesForPathsFailsOnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "unreadable")
	if err := os.WriteFile(unreadable, []byte("secret"), 0o000); err != nil {
		t.Fatalf("write unreadable file: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })

	if _, err := cacheManifestHashesForPaths([]string{unreadable}); err == nil {
		t.Fatal("cacheManifestHashesForPaths() error = nil, want failure on an existing unreadable file")
	}
}

type cacheManifestTestResult struct {
	status       int
	stdout       string
	stderr       string
	githubOutput string
	outputs      map[string]string
}

func runCacheManifestForTest(t *testing.T, env map[string]string) cacheManifestTestResult {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	outputPath := filepath.Join(t.TempDir(), "github_output.txt")
	testEnv := make(map[string]string, len(env)+1)
	for key, value := range env {
		testEnv[key] = value
	}
	testEnv["GITHUB_OUTPUT"] = outputPath

	status := runCacheManifestWith(cacheManifestConfig{
		getenv: func(key string) string {
			return testEnv[key]
		},
		stdout: func(text string) {
			stdout.WriteString(text)
		},
		stderr: func(text string) {
			stderr.WriteString(text)
		},
		executableHash: func() (cacheManifestFileHash, error) {
			return cacheManifestFileHash{
				path:   "/tmp/go-mk-test",
				digest: strings.Repeat("e", 64),
			}, nil
		},
	})

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read GITHUB_OUTPUT: %v", err)
	}
	githubOutput := string(data)
	return cacheManifestTestResult{
		status:       status,
		stdout:       stdout.String(),
		stderr:       stderr.String(),
		githubOutput: githubOutput,
		outputs:      parseGitHubOutputHeredocs(t, githubOutput),
	}
}

func cacheManifestTestRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	return repoDir
}

func cacheManifestGit(t *testing.T, repoDir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repoDir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\noutput:\n%s", strings.Join(args, " "), err, string(output))
	}
}

func writeFakeCacheManifestCompiler(t *testing.T, repoDir string) string {
	t.Helper()
	compilerPath := filepath.Join(repoDir, "fake-cc.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "-dumpmachine" ]]; then
    printf '%s\n' 'arm64-apple-darwin'
    exit 0
fi

if [[ "${1:-}" == "--version" ]]; then
    printf '%s\n' 'fake cc 1.0'
    printf '%s\n' 'extra version line'
    exit 0
fi

printf '%s\n' "unexpected fake compiler argument: ${1:-}" >&2
exit 2
`
	if err := os.WriteFile(compilerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake compiler: %v", err)
	}
	return compilerPath
}

func parseGitHubOutputHeredocs(t *testing.T, output string) map[string]string {
	t.Helper()
	parsed := make(map[string]string)
	lines := strings.Split(output, "\n")
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		name, ok := strings.CutSuffix(line, "<<__GO_MK_CACHE_OUTPUT__")
		if !ok {
			if line == "" {
				continue
			}
			t.Fatalf("unexpected GITHUB_OUTPUT line %q in:\n%s", line, output)
		}
		index++
		valueLines := make([]string, 0)
		for index < len(lines) && lines[index] != "__GO_MK_CACHE_OUTPUT__" {
			valueLines = append(valueLines, lines[index])
			index++
		}
		if index >= len(lines) {
			t.Fatalf("unterminated heredoc for %s in:\n%s", name, output)
		}
		parsed[name] = strings.Join(valueLines, "\n")
	}
	return parsed
}

func githubOutputNames(t *testing.T, output string) []string {
	t.Helper()
	lines := strings.Split(output, "\n")
	names := make([]string, 0)
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		name, ok := strings.CutSuffix(line, "<<__GO_MK_CACHE_OUTPUT__")
		if !ok {
			continue
		}
		names = append(names, name)
		for index < len(lines) && lines[index] != "__GO_MK_CACHE_OUTPUT__" {
			index++
		}
	}
	return names
}
