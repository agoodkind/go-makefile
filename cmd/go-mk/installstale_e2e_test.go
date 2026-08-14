package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installEngineMarker is the engine invocation the primary install rule runs.
// Its presence in a dry run is the observable that says make decided an
// installed binary is stale.
const installEngineMarker = `go-mk" install`

// demoMain is a main package that passes the gate's boundary-event rule.
const demoMain = "package main\n\nimport \"log/slog\"\n\nfunc main() {\n\tslog.Info(\"demo start\")\n}\n"

// staleConsumer is a generated consumer repository under test.
type staleConsumer struct {
	dir        string
	installDir string
}

// newStaleConsumer writes a binary-mode consumer that includes the repository's
// own go.mk and go-build.mk. Extra Makefile lines land before the include, so a
// case can declare additional binaries or codegen.
func newStaleConsumer(t *testing.T, extraMakefileLines string) staleConsumer {
	t.Helper()
	repoRoot := repoRootForTest(t)
	consumerDir := t.TempDir()
	installDir := filepath.Join(consumerDir, "bin")

	makeDir := filepath.Join(consumerDir, ".make")
	if err := os.MkdirAll(makeDir, 0o755); err != nil {
		t.Fatalf("create consumer .make: %v", err)
	}
	if err := os.WriteFile(filepath.Join(makeDir, "golangci.yml"), []byte("version: \"2\"\n"), 0o644); err != nil {
		t.Fatalf("seed golangci configuration: %v", err)
	}
	// GO_MK_SKIP_FETCH requires every opted-in module to be vendored already.
	if err := os.Symlink(filepath.Join(repoRoot, "go-build.mk"), filepath.Join(makeDir, "go-build.mk")); err != nil {
		t.Fatalf("vendor go-build.mk: %v", err)
	}

	writeConsumerFile(t, consumerDir, "cmd/demo/main.go", demoMain)
	writeConsumerFile(t, consumerDir, "go.mod", "module demo\n\ngo 1.26\n")

	makefile := "GO_MK_DEV_DIR := " + repoRoot + "\n" +
		"GO_MK_SKIP_FETCH := 1\n" +
		"GO_MK_MODULES := go-build.mk\n" +
		"BINARY := demo\n" +
		"CMD := ./cmd/demo\n" +
		"INSTALL_DIR := " + installDir + "\n" +
		extraMakefileLines +
		"include " + filepath.Join(repoRoot, "go.mk") + "\n"
	writeConsumerFile(t, consumerDir, "Makefile", makefile)

	return staleConsumer{dir: consumerDir, installDir: installDir}
}

func writeConsumerFile(t *testing.T, consumerDir, relativePath, content string) {
	t.Helper()
	fullPath := filepath.Join(consumerDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", relativePath, err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relativePath, err)
	}
}

// placeInstalled writes a stand-in for an installed binary and dates it forward
// so make sees it as newer than everything written before it.
func (consumer staleConsumer) placeInstalled(t *testing.T, name string, offset time.Duration) string {
	t.Helper()
	// The build-settings stamp is named for the settings and the source list,
	// so it has to be written after the case has seeded its files and before
	// any output can look current.
	consumer.runMake(t, "_go-mk-settings-stamp")
	path := filepath.Join(consumer.installDir, name)
	if err := os.MkdirAll(consumer.installDir, 0o755); err != nil {
		t.Fatalf("create install directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("binary\n"), 0o755); err != nil {
		t.Fatalf("write installed binary %s: %v", name, err)
	}
	touch(t, path, offset)
	return path
}

// dryRunInstall asks make what install would do without running it, which is the
// same staleness decision a real run makes.
func (consumer staleConsumer) dryRunInstall(t *testing.T, makeArgs ...string) string {
	t.Helper()
	return consumer.runMake(t, append([]string{"-n", "install"}, makeArgs...)...)
}

func (consumer staleConsumer) runMake(t *testing.T, makeArgs ...string) string {
	t.Helper()
	command := exec.Command("make", makeArgs...)
	command.Dir = consumer.dir
	command.Env = consumerEnv()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s: %v\n%s", strings.Join(makeArgs, " "), err, output)
	}
	return string(output)
}

// inheritedPrefixes name the variables go.mk exports for its own run. The suite
// itself runs under `make test`, so they are in this process's environment, and
// a `?=` assignment in the consumer would lose to them. Passing the repository's
// lint and vet targets into a temporary consumer makes go list report a package
// that does not exist there.
var inheritedPrefixes = []string{
	"GO_MK_", "GO_BUILD_", "GO_TEST_", "GO_VET_", "GO_INSTALL_",
	"GOLANGCI_", "GOCYCLO_", "DEADCODE_", "STATICCHECK_", "GOVULNCHECK_",
	"BASELINE_", "MAKEFLAGS", "MFLAGS", "MAKELEVEL",
}

// consumerEnv returns this process's environment without the variables go.mk
// exports, so a generated consumer resolves its own settings. GOWORK is off
// because the consumer is a standalone module outside any inherited workspace.
func consumerEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		inherited := false
		for _, prefix := range inheritedPrefixes {
			if strings.HasPrefix(name, prefix) {
				inherited = true
				break
			}
		}
		if !inherited {
			env = append(env, entry)
		}
	}
	return append(env, "GOWORK=off")
}

// touch dates a path forward without the test waiting for real clock movement.
func touch(t *testing.T, path string, offset time.Duration) {
	t.Helper()
	stamp := time.Now().Add(offset)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

func requireInstalls(t *testing.T, output, reason string) {
	t.Helper()
	if !strings.Contains(output, installEngineMarker) {
		t.Fatalf("%s; got:\n%s", reason, output)
	}
}

func requireSkips(t *testing.T, output, reason string) {
	t.Helper()
	if strings.Contains(output, installEngineMarker) {
		t.Fatalf("%s; got:\n%s", reason, output)
	}
}

func TestInstallRebuildsOnlyWhenSourcesAreNewer(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	mainGo := filepath.Join(consumer.dir, "cmd", "demo", "main.go")

	output := consumer.dryRunInstall(t)
	requireInstalls(t, output, "missing installed binary should install")

	// Codegen and workspace routing must be ordered before the compile, not
	// merely before the install wrapper.
	workspaceIndex := strings.Index(output, "go-mk-workspace")
	engineIndex := strings.Index(output, installEngineMarker)
	if workspaceIndex < 0 || workspaceIndex > engineIndex {
		t.Fatalf("go-mk-workspace must run before the engine install; got:\n%s", output)
	}

	installedBin := consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "fresh installed binary should not reinstall")

	touch(t, mainGo, 2*time.Hour)
	requireInstalls(t, consumer.dryRunInstall(t), "changed source should reinstall")

	touch(t, installedBin, 5*time.Hour)
	if err := os.Remove(installedBin); err != nil {
		t.Fatalf("remove installed binary: %v", err)
	}
	requireInstalls(t, consumer.dryRunInstall(t), "removed installed binary should reinstall")
}

// TestInstallIgnoresNestedMetadataDirectories covers a nested module or fixture
// that carries a .git or .make directory of its own.
func TestInstallIgnoresNestedMetadataDirectories(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	writeConsumerFile(t, consumer.dir, "nested/.git/junk.go", demoMain)
	writeConsumerFile(t, consumer.dir, "nested/.make/junk.go", demoMain)

	consumer.placeInstalled(t, "demo", time.Hour)
	touch(t, filepath.Join(consumer.dir, "nested", ".git", "junk.go"), 2*time.Hour)
	touch(t, filepath.Join(consumer.dir, "nested", ".make", "junk.go"), 2*time.Hour)

	requireSkips(t, consumer.dryRunInstall(t), "nested metadata directories should not reinstall")
}

// TestInstallRestoresEveryDeclaredBinary covers INSTALL_BINS, where the primary
// binary can be current while another declared one is missing.
func TestInstallRestoresEveryDeclaredBinary(t *testing.T) {
	consumer := newStaleConsumer(t, "INSTALL_BINS := demo:./cmd/demo helper:./cmd/helper\n")
	writeConsumerFile(t, consumer.dir, "cmd/helper/main.go", demoMain)

	consumer.placeInstalled(t, "demo", time.Hour)
	helperBin := consumer.placeInstalled(t, "helper", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "both binaries present should not reinstall")

	if err := os.Remove(helperBin); err != nil {
		t.Fatalf("remove helper binary: %v", err)
	}
	requireInstalls(t, consumer.dryRunInstall(t), "missing secondary binary should reinstall")
}

// TestInstallRebuildsWhenGeneratedSourceIsMissing covers a declared codegen
// output that go list cannot report because the file does not exist yet.
func TestInstallRebuildsWhenGeneratedSourceIsMissing(t *testing.T) {
	consumer := newStaleConsumer(t,
		"GO_MK_GENERATE := demo-codegen\n"+
			"GO_MK_GENERATE_OUTPUTS := gen/gen.go\n")
	writeConsumerFile(t, consumer.dir, "gen/gen.go", "package gen\n\nconst Name = \"generated\"\n")

	// The codegen target itself has to come after the include, so it is
	// appended rather than passed as an extra line.
	makefilePath := filepath.Join(consumer.dir, "Makefile")
	existing, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read consumer Makefile: %v", err)
	}
	codegen := "\n.PHONY: demo-codegen\ndemo-codegen:\n\t@mkdir -p gen\n" +
		"\t@test -f gen/gen.go || printf 'package gen\\n\\nconst Name = \"generated\"\\n' > gen/gen.go\n"
	if err := os.WriteFile(makefilePath, append(existing, []byte(codegen)...), 0o644); err != nil {
		t.Fatalf("append codegen target: %v", err)
	}

	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "present generated source should not reinstall")

	if err := os.Remove(filepath.Join(consumer.dir, "gen", "gen.go")); err != nil {
		t.Fatalf("remove generated source: %v", err)
	}
	requireInstalls(t, consumer.dryRunInstall(t), "missing generated source should reinstall")
}

// TestInstallRebuildsWhenBuildSettingsChange covers settings that change the
// artifact without touching any source file. The stamp is materialized with a
// real make run because a dry run would not write it.
func TestInstallRebuildsWhenBuildSettingsChange(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "unchanged build settings should not reinstall")

	requireInstalls(t, consumer.dryRunInstall(t, "GO_BUILD_TAGS=demotag"),
		"changed build tags should reinstall")
}

// TestInstallRebuildsForAnotherTargetPlatform covers GOOS and GOARCH, which
// select which files a package compiles. go list reports only the files the
// current context selects, so the target platform has to reach the stamp.
func TestInstallRebuildsForAnotherTargetPlatform(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "unchanged target platform should not reinstall")

	otherGOOS := "linux"
	if strings.TrimSpace(runtimeGOOS(t, consumer)) == "linux" {
		otherGOOS = "darwin"
	}
	requireInstalls(t, consumer.dryRunInstall(t, "GOOS="+otherGOOS),
		"another target platform should reinstall")
}

func runtimeGOOS(t *testing.T, consumer staleConsumer) string {
	t.Helper()
	command := exec.Command("go", "env", "GOOS")
	command.Dir = consumer.dir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go env GOOS: %v", err)
	}
	return string(output)
}

// TestInstallAlwaysRunsWhenHooksAreDeclared covers GO_MK_INSTALL_PRE_CMD and
// GO_MK_INSTALL_POST_CMD, whose side effects a consumer expects on every
// install rather than only when the binary changed.
func TestInstallAlwaysRunsWhenHooksAreDeclared(t *testing.T) {
	consumer := newStaleConsumer(t, "GO_MK_INSTALL_POST_CMD := true\n")
	consumer.placeInstalled(t, "demo", time.Hour)

	requireInstalls(t, consumer.dryRunInstall(t),
		"a declared install hook should keep install running")
}

// TestInstallRebuildsWhenCodegenInputIsAdded covers a new file under a declared
// codegen input directory, which changes no existing file's timestamp.
func TestInstallRebuildsWhenCodegenInputIsAdded(t *testing.T) {
	consumer := newStaleConsumer(t, "GO_MK_GENERATE_INPUTS := grammars\n")
	writeConsumerFile(t, consumer.dir, "grammars/demo.json", "{}\n")

	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "unchanged codegen inputs should not reinstall")

	writeConsumerFile(t, consumer.dir, "grammars/extra.json", "{}\n")
	touch(t, filepath.Join(consumer.dir, "grammars"), 2*time.Hour)
	requireInstalls(t, consumer.dryRunInstall(t), "an added codegen input should reinstall")
}

// TestInstallRunsWhenASourcePathIsUnsupported covers a source path make cannot
// carry as a prerequisite. Losing the skip is the safe direction, so the whole
// discovered list is discarded and install runs every time.
func TestInstallRunsWhenASourcePathIsUnsupported(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "supported paths should allow a skip")

	writeConsumerFile(t, consumer.dir, "cmd/demo/extra file.go", "package main\n")
	requireInstalls(t, consumer.dryRunInstall(t), "an unsupported source path should install")
}

// TestInstallRebuildsWhenGitIdentityChanges covers the commit, version, and
// dirty flag that the engine stamps into the binary. They reach no source file,
// so the stamp is what makes the reported identity match the tree.
func TestInstallRebuildsWhenGitIdentityChanges(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "an unchanged commit should not reinstall")

	requireInstalls(t, consumer.dryRunInstall(t, "GIT_COMMIT=deadbee"),
		"another commit should reinstall")
	// The consumer is outside any git checkout, so GIT_DIRTY already reads
	// true and false is the value that differs.
	requireInstalls(t, consumer.dryRunInstall(t, "GIT_DIRTY=false"),
		"another dirty state should reinstall")
}

// TestInstallRebuildsWhenEntitlementsChange covers the codesign entitlements
// file, whose contents decide what the signed binary may do.
func TestInstallRebuildsWhenEntitlementsChange(t *testing.T) {
	consumer := newStaleConsumer(t, "CODESIGN_ENTITLEMENTS := demo.entitlements\n")
	writeConsumerFile(t, consumer.dir, "demo.entitlements", "<plist/>\n")

	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "unchanged entitlements should not reinstall")

	touch(t, filepath.Join(consumer.dir, "demo.entitlements"), 2*time.Hour)
	requireInstalls(t, consumer.dryRunInstall(t), "changed entitlements should reinstall")
}

// TestBuildConfigStampKeepsShellSyntaxLiteral covers consumer values that reach
// the stamp, such as install hooks. The stamp recipe puts them in a shell
// command, so shell syntax inside them has to stay text.
func TestBuildConfigStampKeepsShellSyntaxLiteral(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	marker := filepath.Join(consumer.dir, "executed")
	hook := "echo hook `touch " + marker + "` ; touch " + marker
	consumer.runMake(t, "_go-mk-settings-stamp", "GO_MK_INSTALL_POST_CMD="+hook)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stamp executed shell syntax from a consumer value: %v", err)
	}
}

// stampName returns the single file in the build-config directory, whose name
// carries the settings.
func (consumer staleConsumer) stampName(t *testing.T, makeArgs ...string) string {
	t.Helper()
	consumer.runMake(t, append([]string{"_go-mk-settings-stamp"}, makeArgs...)...)
	entries, err := os.ReadDir(filepath.Join(consumer.dir, ".make", "build-config"))
	if err != nil {
		t.Fatalf("read build-config directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("build-config holds %d files, want 1", len(entries))
	}
	return entries[0].Name()
}

// TestBuildConfigStampSeparatesEscapedCharacters covers settings that differ
// only by a character the stamp cannot pass through a shell unchanged. Those
// characters are spelled out rather than dropped, so the names stay distinct.
func TestBuildConfigStampSeparatesEscapedCharacters(t *testing.T) {
	consumer := newStaleConsumer(t, "")

	plain := consumer.stampName(t, `GO_BUILD_EXTRA_FLAGS=-X a=b`)
	escaped := consumer.stampName(t, `GO_BUILD_EXTRA_FLAGS=-X a=\b`)
	if plain == escaped {
		t.Fatalf("settings differing by a backslash share the stamp name %q", plain)
	}
}

// TestInstallRebuildsWhenASourceIsDeleted covers a deleted source file. A
// timestamp comparison sees only files that still exist, so the source list is
// part of the stamp.
func TestInstallRebuildsWhenASourceIsDeleted(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	writeConsumerFile(t, consumer.dir, "cmd/demo/extra.go", "package main\n\nconst extra = 1\n")
	writeConsumerFile(t, consumer.dir, "cmd/demo/main.go",
		"package main\n\nimport \"log/slog\"\n\nfunc main() {\n\tslog.Info(\"demo\", slog.Int(\"extra\", extra))\n}\n")

	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "an unchanged source set should not reinstall")

	if err := os.Remove(filepath.Join(consumer.dir, "cmd", "demo", "extra.go")); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	requireInstalls(t, consumer.dryRunInstall(t), "a deleted source should reinstall")
}

// TestInstallRunsWhenAPackageFailsToLoad covers a package go list cannot load.
// go list -e reports it instead of failing, so the remaining packages would
// otherwise look like the whole answer.
func TestInstallRunsWhenAPackageFailsToLoad(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	writeConsumerFile(t, consumer.dir, "broken/broken.go", "package broken\n\nimport \"demo/absent\"\n\nvar _ = absent.Name\n")

	consumer.placeInstalled(t, "demo", time.Hour)
	requireInstalls(t, consumer.dryRunInstall(t), "a package that fails to load should install")
}

// TestGenerateInputsKeepShellSyntaxLiteral covers a declared codegen path that
// carries shell syntax. Paths reach a shell before the filter sees them, so
// each one is quoted.
func TestGenerateInputsKeepShellSyntaxLiteral(t *testing.T) {
	consumer := newStaleConsumer(t, "")
	marker := filepath.Join(consumer.dir, "executed")
	// The trailing `true` swallows the find arguments that follow the injected
	// command, so the touch would run if the path reached the shell unquoted.
	consumer.runMake(t, "_go-mk-settings-stamp",
		"GO_MK_GENERATE_INPUTS=grammars;touch "+marker+";true")

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("a declared codegen path ran as a command: %v", err)
	}

	// A single quote in the value ends the quoting unless it is escaped. This
	// injection holds no space, so make keeps it as one word and the quoting is
	// the only thing standing between it and the shell.
	quoted := filepath.Join(consumer.dir, "quoted")
	consumer.runMake(t, "_go-mk-settings-stamp",
		"GO_MK_GENERATE_INPUTS=grammars';>"+quoted+";true'")

	if _, err := os.Stat(quoted); !os.IsNotExist(err) {
		t.Fatalf("a quote in a declared codegen path ran as a command: %v", err)
	}
}

// TestInstallRunsWhenADeclaredInputIsMissing covers a declared input that does
// not exist. Make cannot mark an output stale against a file it cannot see, so
// the outputs become always-run instead.
func TestInstallRunsWhenADeclaredInputIsMissing(t *testing.T) {
	consumer := newStaleConsumer(t, "CODESIGN_ENTITLEMENTS := demo.entitlements\n")
	writeConsumerFile(t, consumer.dir, "demo.entitlements", "<plist/>\n")

	consumer.placeInstalled(t, "demo", time.Hour)
	requireSkips(t, consumer.dryRunInstall(t), "a present entitlements file should allow a skip")

	if err := os.Remove(filepath.Join(consumer.dir, "demo.entitlements")); err != nil {
		t.Fatalf("remove entitlements: %v", err)
	}
	requireInstalls(t, consumer.dryRunInstall(t), "a missing entitlements file should install")
}
