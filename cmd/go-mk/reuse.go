// Build-output reuse for go-mk. A consumer that opts in skips the whole build
// path, gate included, when nothing that feeds it has changed since the last
// successful run.
//
// The decision is content-addressed, never timestamp-addressed. A stamp records
// one fingerprint of every build input plus the digest of every output the run
// produced; the next run recomputes both and proceeds only on an exact match.
// That is what makes a deleted source file, a rewritten binary, or a changed
// engine visible, none of which a modification time can express.
//
// Every uncertain case rebuilds. A missing stamp, an unreadable file, a package
// the Go toolchain could not load, or an output that no longer matches its
// recorded digest all fall through to the normal build.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// reuseStampVersion changes whenever the fingerprint's meaning changes, so a
// stamp written by an older engine can never satisfy a newer one.
const reuseStampVersion = "1"

// reuseStampFile holds one record per mode, beside the other engine state a
// consumer keeps in .make.
const reuseStampFile = ".make/go-mk-reuse.json"

// reuseEnvPrefixes name the environment variables that change what a build or
// its gate does. Matching by prefix rather than by an explicit list means a new
// setting in the make layer invalidates the stamp without anyone remembering to
// add it here.
var reuseEnvPrefixes = []string{
	"BINARY", "BUNDLE_ID", "CGO_ENABLED", "CC", "CMD", "CODESIGN_", "CXX",
	"DEADCODE_", "DIST_DIR", "GKLOG_VPKG", "GOARCH", "GOCYCLO_", "GOEXPERIMENT",
	"GOFLAGS", "GOOS", "GOWORK", "GO_BUILD_", "GO_MK_", "GO_TEST_", "GO_VET_",
	"GOLANGCI", "GOVULNCHECK", "INSTALL_BINS", "INSTALL_DIR", "LINT_",
	"RELEASE_BINS", "STATICCHECK", "VPKG",
}

// reuseConfigFiles are repo-root files whose content changes how the build or
// the gate behaves but which no package's file list reports.
var reuseConfigFiles = []string{
	".deadcode-baseline.txt",
	".gocyclo-baseline.txt",
	".golangci-lint-baseline.txt",
	".make/golangci.yml",
	".staticcheck-extra-baseline.txt",
	"Makefile",
	"bootstrap.mk",
	"config.mk",
	"go.mod",
	"go.sum",
	"go.work",
	"go.work.sum",
}

// reuseTruthyValues are the ways a consumer can spell "on" in GO_MK_REUSE_OUTPUTS.
var reuseTruthyValues = map[string]struct{}{
	"1": {}, "true": {}, "yes": {}, "on": {},
}

// buildTimePattern matches the version stamp that carries the moment of the
// build. It is an output of the build, not an input to it, so leaving it in the
// fingerprint would make every run differ from the last and reuse impossible.
var buildTimePattern = regexp.MustCompile(`-X\s+\S*\.BuildTime=\S*`)

// reuseRecord is one mode's stamp: the fingerprint of the inputs and the digest
// of every output the recorded run produced.
type reuseRecord struct {
	Version     string            `json:"version"`
	Fingerprint string            `json:"fingerprint"`
	Outputs     map[string]string `json:"outputs"`
}

// reuseEnabled reports whether the consumer opted into reuse. It is off unless
// asked for, so no consumer changes behavior until it says so.
func reuseEnabled() bool {
	_, ok := reuseTruthyValues[strings.ToLower(strings.TrimSpace(os.Getenv("GO_MK_REUSE_OUTPUTS")))]
	return ok
}

// runBuildGateCommand is the build-gate entry point a library-mode consumer's
// `build` target calls. A library produces no artifact, so the only thing to
// reuse is the verdict: the gate passed on exactly this tree once already.
func runBuildGateCommand() int {
	empty := installConfig{}
	if reuseSatisfied("build-gate", empty) {
		writeStdout("build-gate reused (no build input changed)\n")
		return 0
	}
	if code := runBuildGateFunc(); code != 0 {
		return code
	}
	recordReuse("build-gate", empty)
	return 0
}

// reuseOutputs lists the files a mode produces. build leaves each binary in the
// dist directory; install also places each one in its target directory. A gate
// produces none, so its stamp rests on the fingerprint alone.
func reuseOutputs(mode string, cfg installConfig) []string {
	paths := make([]string, 0, len(cfg.bins)*2)
	for _, bin := range cfg.bins {
		paths = append(paths, filepath.Join(cfg.distDir, bin.name))
		if mode == "install" {
			paths = append(paths, filepath.Join(bin.dir, bin.name))
		}
	}
	sort.Strings(paths)
	return paths
}

// reuseSatisfied reports whether the recorded run still describes the current
// tree, so the caller can return without building. Any uncertainty answers no.
func reuseSatisfied(mode string, cfg installConfig) bool {
	if !reuseEnabled() {
		return false
	}
	record, ok := readReuseStamp(mode)
	if !ok {
		return false
	}
	fingerprint, err := reuseFingerprint(mode, cfg)
	if err != nil {
		slog.Warn("build reuse could not fingerprint the inputs; building",
			slog.String("mode", mode), slog.String("err", err.Error()))
		return false
	}
	if record.Version != reuseStampVersion || record.Fingerprint != fingerprint {
		return false
	}
	return outputsUnchanged(mode, record.Outputs, reuseOutputs(mode, cfg))
}

// outputsUnchanged reports whether every recorded output is still present with
// the digest the recorded run left behind.
func outputsUnchanged(mode string, recorded map[string]string, wanted []string) bool {
	if len(recorded) != len(wanted) {
		return false
	}
	mismatch := firstOutputMismatch(recorded, wanted)
	if mismatch != "" {
		slog.Info("build reuse output no longer matches its stamp; building",
			slog.String("mode", mode), slog.String("path", mismatch))
		return false
	}
	slog.Info("build reuse outputs all match their stamp",
		slog.String("mode", mode), slog.Int("count", len(wanted)))
	return true
}

// firstOutputMismatch names the first output that is missing, unreadable, or no
// longer the file the recorded run produced, and is empty when all of them are
// intact.
func firstOutputMismatch(recorded map[string]string, wanted []string) string {
	for _, path := range wanted {
		digest, err := fileDigest(path)
		if err != nil || recorded[path] != digest {
			return path
		}
	}
	return ""
}

// recordReuse writes the stamp for a run that just succeeded. A failure to write
// it costs the next run a rebuild and nothing else, so it is reported and not
// returned.
func recordReuse(mode string, cfg installConfig) {
	if !reuseEnabled() {
		return
	}
	fingerprint, err := reuseFingerprint(mode, cfg)
	if err != nil {
		slog.Warn("build reuse could not fingerprint the inputs; not stamping",
			slog.String("mode", mode), slog.String("err", err.Error()))
		return
	}
	outputs := make(map[string]string, len(cfg.bins)*2)
	for _, path := range reuseOutputs(mode, cfg) {
		digest, digestErr := fileDigest(path)
		if digestErr != nil {
			slog.Warn("build reuse could not digest an output; not stamping",
				slog.String("mode", mode), slog.String("path", path),
				slog.String("err", digestErr.Error()))
			return
		}
		outputs[path] = digest
	}
	writeReuseStamp(mode, reuseRecord{
		Version:     reuseStampVersion,
		Fingerprint: fingerprint,
		Outputs:     outputs,
	})
}

// readReuseStamp returns the record for one mode. A missing, unreadable, or
// malformed stamp reports no record, which makes the caller build.
func readReuseStamp(mode string) (reuseRecord, bool) {
	raw, err := os.ReadFile(reuseStampFile)
	if err != nil {
		return reuseRecord{}, false
	}
	stamps := make(map[string]reuseRecord)
	if unmarshalErr := json.Unmarshal(raw, &stamps); unmarshalErr != nil {
		slog.Warn("build reuse stamp is unreadable; building",
			slog.String("path", reuseStampFile), slog.String("err", unmarshalErr.Error()))
		return reuseRecord{}, false
	}
	record, ok := stamps[mode]
	return record, ok
}

// writeReuseStamp merges one mode's record into the stamp file. It writes the
// filesystem, so it emits a boundary log.
func writeReuseStamp(mode string, record reuseRecord) {
	slog.Info("build reuse stamp written",
		slog.String("mode", mode), slog.String("path", reuseStampFile))
	stamps := make(map[string]reuseRecord)
	if raw, err := os.ReadFile(reuseStampFile); err == nil {
		_ = json.Unmarshal(raw, &stamps)
	}
	stamps[mode] = record
	encoded, err := json.MarshalIndent(stamps, "", "  ")
	if err != nil {
		slog.Warn("build reuse could not encode its stamp",
			slog.String("mode", mode), slog.String("err", err.Error()))
		return
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(reuseStampFile), 0o755); mkdirErr != nil {
		slog.Warn("build reuse could not create its stamp directory",
			slog.String("path", filepath.Dir(reuseStampFile)), slog.String("err", mkdirErr.Error()))
		return
	}
	if writeErr := os.WriteFile(reuseStampFile, encoded, 0o644); writeErr != nil {
		slog.Warn("build reuse could not write its stamp",
			slog.String("path", reuseStampFile), slog.String("err", writeErr.Error()))
	}
}

// reuseFingerprint digests everything that decides what a build produces: the
// engine itself, the settings the make layer exports, the resolved binary set,
// every source file the Go toolchain reports, and the config files no package
// lists.
func reuseFingerprint(mode string, cfg installConfig) (string, error) {
	sources, err := reuseSourceFiles()
	if err != nil {
		return "", err
	}
	engine, err := engineDigest()
	if err != nil {
		return "", err
	}

	hash := sha256.New()
	writeFingerprintLine(hash, "version", reuseStampVersion)
	writeFingerprintLine(hash, "mode", mode)
	writeFingerprintLine(hash, "engine", engine)
	for _, entry := range reuseEnvironment() {
		writeFingerprintLine(hash, "env", entry)
	}
	for _, bin := range cfg.bins {
		writeFingerprintLine(hash, "bin", bin.name+"|"+bin.mainPkg+"|"+bin.dir)
	}
	writeFingerprintLine(hash, "dist", cfg.distDir)
	if err := hashFingerprintFiles(hash, "source", sources); err != nil {
		return "", err
	}
	if err := hashFingerprintFiles(hash, "config", existingConfigFiles()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// hashFingerprintFiles folds each named file's path and digest into the hash. A
// file that cannot be read is an unknown input, so it fails the fingerprint and
// the caller builds.
func hashFingerprintFiles(hash io.Writer, label string, paths []string) error {
	for _, path := range paths {
		digest, err := fileDigest(path)
		if err != nil {
			slog.Warn("build reuse could not read an input; building",
				slog.String("label", label), slog.String("path", path),
				slog.String("err", err.Error()))
			return err
		}
		writeFingerprintLine(hash, label, path+" "+digest)
	}
	return nil
}

// writeFingerprintLine appends one labelled record. The newline and the label
// keep two different fields from ever producing the same byte stream.
func writeFingerprintLine(hash io.Writer, label string, value string) {
	_, _ = io.WriteString(hash, label+"\x00"+value+"\n")
}

// existingConfigFiles returns the config files that are present, plus every
// make fragment at the root, since a consumer can name those anything.
func existingConfigFiles() []string {
	found := make([]string, 0, len(reuseConfigFiles))
	for _, name := range reuseConfigFiles {
		if _, err := os.Stat(name); err == nil {
			found = append(found, name)
		}
	}
	fragments, err := filepath.Glob("*.mk")
	if err == nil {
		found = append(found, fragments...)
	}
	return sortedUnique(found)
}

// engineDigest digests the running go-mk binary, so any change to the engine,
// including a changed lint rule compiled into it, invalidates every stamp.
func engineDigest() (string, error) {
	self, err := os.Executable()
	if err != nil {
		slog.Warn("build reuse could not locate the engine binary",
			slog.String("err", err.Error()))
		return "", err
	}
	return fileDigest(self)
}

// fileDigest returns the hex sha256 of one file. It reads the filesystem, and
// is called once per input, so it does not log per call.
func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, copyErr := io.Copy(hash, file); copyErr != nil {
		return "", copyErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// reuseEnvironment returns the sorted NAME=VALUE entries that matter to a build,
// with the build timestamp removed from the version stamp because it is an
// output of the run rather than an input to it.
func reuseEnvironment() []string {
	entries := make([]string, 0, 32)
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if !found || !hasReusePrefix(name) {
			continue
		}
		if name == "GO_BUILD_LDFLAGS" {
			value = normalizeLdflags(value)
		}
		entries = append(entries, name+"="+value)
	}
	sort.Strings(entries)
	return entries
}

// hasReusePrefix reports whether an environment variable name is one the build
// or its gate reads.
func hasReusePrefix(name string) bool {
	for _, prefix := range reuseEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// normalizeLdflags removes the build-time stamp so two runs of an unchanged tree
// produce the same fingerprint.
func normalizeLdflags(ldflags string) string {
	return strings.Join(strings.Fields(buildTimePattern.ReplaceAllString(ldflags, " ")), " ")
}

// reuseSourceFiles returns every file the build and test graph is composed of,
// sorted. Paths stay as the toolchain reports them, so a file outside the
// current directory, such as a workspace sibling, is fingerprinted rather than
// silently dropped.
//
// A package the toolchain could not load makes the list incomplete, and an
// incomplete list would let a real change go unnoticed, so that is an error.
func reuseSourceFiles() ([]string, error) {
	slog.Info("build reuse list packages")
	out, err := exec.Command("go", "list", "-e", "-deps", "-json", "./...").Output()
	if err != nil {
		slog.Warn("build reuse could not list packages; building",
			slog.String("err", err.Error()))
		return nil, err
	}
	files, decodeErr := decodeReuseSourceFiles(out)
	if decodeErr != nil {
		slog.Warn("build reuse could not read the package list; building",
			slog.String("err", decodeErr.Error()))
		return nil, decodeErr
	}
	return files, nil
}

// decodeReuseSourceFiles turns the go list stream into the sorted file set.
func decodeReuseSourceFiles(out []byte) ([]string, error) {
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	files := make([]string, 0, 256)
	for decoder.More() {
		var pkg goListPackage
		if decodeErr := decoder.Decode(&pkg); decodeErr != nil {
			return nil, decodeErr
		}
		if pkg.Standard || pkg.Dir == "" {
			continue
		}
		if pkg.Error != nil || len(pkg.DepsErrors) > 0 {
			return nil, errReusePackageLoad
		}
		for _, group := range packageFileGroups(pkg) {
			for _, name := range group {
				files = append(files, filepath.ToSlash(filepath.Join(pkg.Dir, name)))
			}
		}
	}
	return sortedUnique(files), nil
}

const errReusePackageLoad sentinelError = "build reuse: the Go toolchain could not load a package, so the input list is incomplete"
