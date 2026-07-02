package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const cacheManifestOutputDelimiter = "__GO_MK_CACHE_OUTPUT__"

var cacheManifestTrackedPathspecs = []string{
	"Makefile",
	"*.mk",
	"go.mod",
	"go.sum",
	"go.work",
	"go.work.sum",
	".gitmodules",
	"buf.yaml",
	"buf.gen.yaml",
}

var cacheManifestOutputNames = []string{
	"generated_cache_enabled",
	"generated_cache_requires_submodules",
	"generated_cache_paths",
	"generated_cache_key",
	"generated_cache_warnings",
}

type cacheManifestConfig struct {
	getenv         func(string) string
	stdout         func(string)
	stderr         func(string)
	executableHash func() (cacheManifestFileHash, error)
}

type cacheManifestFileHash struct {
	path   string
	digest string
}

type cacheManifestResult struct {
	enabled            string
	requiresSubmodules string
	paths              string
	key                string
	warnings           string
}

func runCacheManifest() int {
	return runCacheManifestWith(cacheManifestConfig{
		getenv:         os.Getenv,
		stdout:         writeStdout,
		stderr:         writeStderr,
		executableHash: runningExecutableHash,
	})
}

func runCacheManifestWith(config cacheManifestConfig) int {
	config = normalizeCacheManifestConfig(config)
	outputPaths, warnings := filterCacheManifestGeneratedOutputs(
		config.getenv("GO_MK_GENERATE_OUTPUTS"),
		config.stderr,
	)

	enabled := "false"
	if config.getenv("GO_MK_GENERATE") != "" &&
		config.getenv("GO_MK_GENERATE_INPUTS") != "" &&
		outputPaths != "" {
		enabled = "true"
	}

	requiresSubmodules := "false"
	if enabled == "true" && fileExists(".gitmodules") {
		requiresSubmodules = "true"
	}

	manifest, manifestErr := buildCacheManifest(config, outputPaths)
	if manifestErr != nil {
		config.stderr("go-mk-cache-manifest: " + manifestErr.Error() + "\n")
		return 1
	}
	keyBytes := sha256.Sum256([]byte(manifest))
	result := cacheManifestResult{
		enabled:            enabled,
		requiresSubmodules: requiresSubmodules,
		paths:              outputPaths,
		key:                hex.EncodeToString(keyBytes[:]),
		warnings:           strings.Join(warnings, "\n"),
	}

	if outputErr := writeCacheManifestGitHubOutputs(config.getenv("GITHUB_OUTPUT"), result); outputErr != nil {
		config.stderr("go-mk-cache-manifest: " + outputErr.Error() + "\n")
		return 1
	}

	config.stdout("go-mk-cache-manifest: generated_cache_enabled=" + result.enabled + "\n")
	config.stdout("go-mk-cache-manifest: generated_cache_requires_submodules=" + result.requiresSubmodules + "\n")
	if result.paths != "" {
		config.stdout("go-mk-cache-manifest: generated output paths:\n" + result.paths + "\n")
	}
	if result.warnings != "" {
		config.stderr(result.warnings + "\n")
	}
	return 0
}

func normalizeCacheManifestConfig(config cacheManifestConfig) cacheManifestConfig {
	if config.getenv == nil {
		config.getenv = os.Getenv
	}
	if config.stdout == nil {
		config.stdout = writeStdout
	}
	if config.stderr == nil {
		config.stderr = writeStderr
	}
	if config.executableHash == nil {
		config.executableHash = runningExecutableHash
	}
	return config
}

func filterCacheManifestGeneratedOutputs(rawOutputs string, stderr func(string)) (string, []string) {
	outputs := make([]string, 0)
	warnings := make([]string, 0)
	for _, output := range normalizeCacheManifestFields(rawOutputs) {
		if output == "" {
			continue
		}
		if cacheManifestPathIsTracked(output) {
			stderr("go-mk-cache-manifest: skip tracked generated output: " + output + "\n")
			warnings = append(warnings, "tracked output skipped: "+output)
			continue
		}
		outputs = append(outputs, output)
	}
	return strings.Join(outputs, "\n"), warnings
}

func buildCacheManifest(config cacheManifestConfig, outputPaths string) (string, error) {
	var builder strings.Builder
	builder.WriteString("repo_prefix\t")
	builder.WriteString(cacheManifestRepoPrefix())
	builder.WriteString("\n")
	builder.WriteString("generate\t")
	builder.WriteString(config.getenv("GO_MK_GENERATE"))
	builder.WriteString("\n")
	builder.WriteString("generate_inputs_begin\n")
	for _, input := range normalizeCacheManifestFields(config.getenv("GO_MK_GENERATE_INPUTS")) {
		builder.WriteString(input)
		builder.WriteString("\n")
	}
	builder.WriteString("generate_inputs_end\n")
	builder.WriteString("generate_outputs_begin\n")
	builder.WriteString(outputPaths)
	builder.WriteString("\n")
	builder.WriteString("generate_outputs_end\n")
	builder.WriteString("workspace_use\t")
	builder.WriteString(config.getenv("GO_MK_WORKSPACE_USE"))
	builder.WriteString("\n")
	builder.WriteString("tree_sitter_abi\t")
	builder.WriteString(config.getenv("TREE_SITTER_ABI"))
	builder.WriteString("\n")
	builder.WriteString("go_mk_api_repo\t")
	builder.WriteString(config.getenv("GO_MK_API_REPO"))
	builder.WriteString("\n")
	builder.WriteString("go_mk_api_ref\t")
	builder.WriteString(config.getenv("GO_MK_API_REF"))
	builder.WriteString("\n")
	for _, outputPath := range strings.Split(outputPaths, "\n") {
		if outputPath == "" {
			continue
		}
		builder.WriteString("output_path\t")
		builder.WriteString(outputPath)
		builder.WriteString("\n")
	}

	builder.WriteString(cacheManifestSubmoduleStatus())
	trackedHashes, trackedErr := collectCacheManifestTrackedHashes(config)
	if trackedErr != nil {
		return "", trackedErr
	}
	for _, hash := range trackedHashes {
		builder.WriteString("file\t")
		builder.WriteString(hash.path)
		builder.WriteString("\t")
		builder.WriteString(hash.digest)
		builder.WriteString("\n")
	}
	implementationHashes, implementationErr := collectCacheManifestImplementationHashes(config)
	if implementationErr != nil {
		return "", implementationErr
	}
	for _, hash := range implementationHashes {
		builder.WriteString("file\t")
		builder.WriteString(hash.path)
		builder.WriteString("\t")
		builder.WriteString(hash.digest)
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

func normalizeCacheManifestFields(value string) []string {
	fields := make([]string, 0)
	for _, field := range strings.Fields(value) {
		if field == "" || field == "." {
			continue
		}
		normalized := collapseCacheManifestSlashes(field)
		normalized = strings.TrimPrefix(normalized, "./")
		normalized = strings.TrimSuffix(normalized, "/")
		fields = append(fields, normalized)
	}
	return fields
}

func collapseCacheManifestSlashes(value string) string {
	var builder strings.Builder
	lastWasSlash := false
	for _, character := range value {
		if character == '/' {
			if !lastWasSlash {
				builder.WriteRune(character)
			}
			lastWasSlash = true
			continue
		}
		builder.WriteRune(character)
		lastWasSlash = false
	}
	return builder.String()
}

func cacheManifestRepoPrefix() string {
	slog.Info("cache-manifest git repo prefix")
	output, err := exec.Command("git", "rev-parse", "--show-prefix").Output()
	if err != nil {
		return ""
	}
	prefix := strings.TrimRight(string(output), "\n")
	return strings.TrimSuffix(prefix, "/")
}

func cacheManifestPathIsTracked(path string) bool {
	slog.Info("cache-manifest check tracked path", slog.String("path", path))
	parent := deepestCacheManifestExistingParent(path)
	if parent == "" {
		return false
	}
	gitRootOutput, rootErr := exec.Command("git", "-C", parent, "rev-parse", "--show-toplevel").Output()
	if rootErr != nil {
		return false
	}
	gitRoot := strings.TrimRight(string(gitRootOutput), "\n")
	if gitRoot == "" {
		return false
	}
	absolutePath := path
	if !filepath.IsAbs(path) {
		cwd, cwdErr := physicalWorkingDirectory()
		if cwdErr != nil {
			return false
		}
		absolutePath = filepath.Join(cwd, path)
	}
	relativePath := strings.TrimPrefix(absolutePath, gitRoot+string(os.PathSeparator))
	if exec.Command("git", "-C", gitRoot, "ls-files", "--error-unmatch", relativePath).Run() == nil {
		return true
	}
	output, outputErr := exec.Command("git", "-C", gitRoot, "ls-files", "--", relativePath).Output()
	if outputErr != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != ""
}

func deepestCacheManifestExistingParent(path string) string {
	parent := filepath.Dir(path)
	for parent != "." && parent != string(os.PathSeparator) {
		if _, err := os.Stat(parent); err == nil {
			return parent
		}
		parent = filepath.Dir(parent)
	}
	if _, err := os.Stat("."); err == nil {
		return "."
	}
	return ""
}

func physicalWorkingDirectory() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	physical, evalErr := filepath.EvalSymlinks(cwd)
	if evalErr != nil {
		return cwd, nil
	}
	return physical, nil
}

func cacheManifestSubmoduleStatus() string {
	if !fileExists(".gitmodules") {
		return ""
	}
	slog.Info("cache-manifest git submodule status")
	var builder strings.Builder
	builder.WriteString("submodules_begin\n")
	output, _ := exec.Command("git", "submodule", "status", "--recursive").Output()
	builder.WriteString(string(output))
	builder.WriteString("submodules_end\n")
	return builder.String()
}

func collectCacheManifestTrackedHashes(config cacheManifestConfig) ([]cacheManifestFileHash, error) {
	paths := make([]string, 0)
	paths = append(paths, cacheManifestGitLsFiles(cacheManifestTrackedPathspecs...)...)
	for _, input := range normalizeCacheManifestFields(config.getenv("GO_MK_GENERATE_INPUTS")) {
		if input == "" {
			continue
		}
		paths = append(paths, cacheManifestGitLsFiles(input)...)
	}
	return cacheManifestHashesForPaths(paths)
}

// executableManifestLabel is the stable path label recorded for the running
// go-mk binary in the manifest. The real executable bytes are still hashed;
// only the label is fixed, so the cache key does not vary with the checkout or
// workspace directory the binary happens to run from.
const executableManifestLabel = "go-mk-executable"

func cacheManifestGitLsFiles(pathspecs ...string) []string {
	slog.Info("cache-manifest git ls-files", slog.Any("pathspecs", pathspecs))
	args := []string{"ls-files", "--"}
	args = append(args, pathspecs...)
	output, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	return splitNonEmptyCacheManifestLines(string(output))
}

func collectCacheManifestImplementationHashes(config cacheManifestConfig) ([]cacheManifestFileHash, error) {
	hashes := make([]cacheManifestFileHash, 0)
	executableHash, executableErr := config.executableHash()
	if executableErr != nil {
		return nil, executableErr
	}
	hashes = append(hashes, executableHash)
	candidatePaths := []string{
		config.getenv("GO_MK_SELF"),
		".make/go.mk",
	}
	candidateHashes, candidateErr := cacheManifestHashesForPaths(candidatePaths)
	if candidateErr != nil {
		return nil, candidateErr
	}
	hashes = append(hashes, candidateHashes...)
	return hashes, nil
}

func cacheManifestHashesForPaths(paths []string) ([]cacheManifestFileHash, error) {
	uniquePaths := cacheManifestSortedUnique(paths)
	hashes := make([]cacheManifestFileHash, 0, len(uniquePaths))
	for _, path := range uniquePaths {
		if path == "" || !fileExists(path) {
			continue
		}
		// A file that exists but cannot be hashed (a permission or read error)
		// must fail the manifest rather than be omitted, so a missing input can
		// never silently reuse a stale cache. This matches the prior shell
		// script's set -euo pipefail behavior.
		fileHash, hashErr := cacheManifestHashFile(path)
		if hashErr != nil {
			return nil, hashErr
		}
		hashes = append(hashes, fileHash)
	}
	return hashes, nil
}

func cacheManifestHashFile(path string) (cacheManifestFileHash, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheManifestFileHash{}, err
	}
	digest := sha256.Sum256(data)
	return cacheManifestFileHash{
		path:   path,
		digest: hex.EncodeToString(digest[:]),
	}, nil
}

func runningExecutableHash() (cacheManifestFileHash, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return cacheManifestFileHash{}, err
	}
	fileHash, hashErr := cacheManifestHashFile(executablePath)
	if hashErr != nil {
		return cacheManifestFileHash{}, hashErr
	}
	fileHash.path = executableManifestLabel
	return fileHash, nil
}

func cacheManifestSortedUnique(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	sort.Strings(unique)
	return unique
}

func splitNonEmptyCacheManifestLines(text string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func writeCacheManifestGitHubOutputs(path string, result cacheManifestResult) error {
	if path == "" {
		return nil
	}
	values := map[string]string{
		"generated_cache_enabled":             result.enabled,
		"generated_cache_requires_submodules": result.requiresSubmodules,
		"generated_cache_paths":               result.paths,
		"generated_cache_key":                 result.key,
		"generated_cache_warnings":            result.warnings,
	}
	delimiter, delimiterErr := cacheManifestOutputDelimiterFor(values)
	if delimiterErr != nil {
		return delimiterErr
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	for _, name := range cacheManifestOutputNames {
		if _, writeErr := fmt.Fprintf(
			file,
			"%s<<%s\n%s\n%s\n",
			name,
			delimiter,
			values[name],
			delimiter,
		); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

// cacheManifestOutputDelimiterFor returns a heredoc delimiter that no value can
// prematurely terminate. A GitHub Actions heredoc ends at a line exactly equal
// to the delimiter, so if any value already contains such a line the stable
// delimiter is extended with random hex until it is collision-free. The common
// case keeps the fixed delimiter, since real values are booleans, a sha256 hex
// key, and file paths.
func cacheManifestOutputDelimiterFor(values map[string]string) (string, error) {
	delimiter := cacheManifestOutputDelimiter
	for cacheManifestValuesContainDelimiter(values, delimiter) {
		suffix, err := cacheManifestRandomSuffix()
		if err != nil {
			return "", err
		}
		delimiter = cacheManifestOutputDelimiter + "-" + suffix
	}
	return delimiter, nil
}

func cacheManifestValuesContainDelimiter(values map[string]string, delimiter string) bool {
	for _, value := range values {
		for _, line := range strings.Split(value, "\n") {
			if line == delimiter {
				return true
			}
		}
	}
	return false
}

func cacheManifestRandomSuffix() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
